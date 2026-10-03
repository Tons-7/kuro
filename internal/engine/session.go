package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"kuro/internal/torrent"
)

// record is one torrent in session.json; the field names are rqbit's, so its
// session reads with the same type.
type record struct {
	InfoHash     string   `json:"info_hash"`
	Trackers     []string `json:"trackers,omitempty"`
	OutputFolder string   `json:"output_folder,omitempty"`
	OnlyFiles    []int    `json:"only_files"`
	Paused       bool     `json:"is_paused"`
	// Verify: data came from elsewhere (rqbit), so completion is unknown until hashed.
	Verify bool `json:"verify,omitempty"`
}

type sessionDoc struct {
	Torrents []record `json:"torrents"`
}

// saveSession writes the torrent list and any metainfo not yet on disk, so a
// restart needs nothing from the swarm before resuming.
func (e *Engine) saveSession() {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	if e.cl == nil {
		e.mu.Unlock()
		return
	}
	var doc sessionDoc
	var withInfo []*atorrent.Torrent
	for _, en := range e.byID {
		r := record{InfoHash: en.t.InfoHash().HexString(), Trackers: en.trackers, Paused: en.paused, Verify: en.verifying}
		for i, on := range en.selected {
			if on {
				r.OnlyFiles = append(r.OnlyFiles, i)
			}
		}
		slices.Sort(r.OnlyFiles)
		doc.Torrents = append(doc.Torrents, r)
		if en.t.Info() != nil {
			withInfo = append(withInfo, en.t)
		}
	}
	e.mu.Unlock()
	slices.SortFunc(doc.Torrents, func(a, b record) int { return strings.Compare(a.InfoHash, b.InfoHash) })

	for _, t := range withInfo {
		path := filepath.Join(e.opts.SessionDir, t.InfoHash().HexString()+".torrent")
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := writeMetainfo(path, t.Metainfo()); err != nil {
			e.log.Warn("save torrent metainfo", "hash", t.InfoHash().HexString(), "err", err)
		}
	}

	body, err := json.MarshalIndent(doc, "", "  ")
	if err == nil {
		err = writeAtomic(sessionFile(e.opts.SessionDir), body)
	}
	if err != nil {
		e.log.Warn("save torrent session", "err", err)
	}
}

func writeMetainfo(path string, mi metainfo.MetaInfo) error {
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return err
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func writeAtomic(path string, body []byte) error {
	if err := os.WriteFile(path+".tmp", body, 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// restore re-adds the saved torrents; on first run it imports rqbit's.
func (e *Engine) restore() error {
	var imported string
	doc, err := readSession(e.opts.SessionDir)
	if errors.Is(err, fs.ErrNotExist) {
		doc, imported, err = e.importLegacy()
		if err != nil {
			return err
		}
		if imported != "" {
			e.log.Info("carried over downloads from rqbit", "torrents", len(doc.Torrents), "from", imported)
		}
	} else if err != nil {
		return err
	}

	for _, r := range doc.Torrents {
		var hash metainfo.Hash
		if err := hash.FromHexString(strings.ToLower(r.InfoHash)); err != nil {
			e.log.Warn("restore torrent: bad info hash", "hash", r.InfoHash, "err", err)
			continue
		}
		var t *atorrent.Torrent
		path := filepath.Join(e.opts.SessionDir, hash.HexString()+".torrent")
		if mi, err := metainfo.LoadFromFile(path); err == nil {
			spec, err := atorrent.TorrentSpecFromMetaInfoErr(mi)
			if err == nil {
				t, _, err = e.cl.AddTorrentSpec(spec)
			}
			if err != nil {
				e.log.Warn("restore torrent", "hash", r.InfoHash, "err", err)
				continue
			}
		} else {
			magnet := (&metainfo.Magnet{InfoHash: hash, Trackers: r.Trackers}).String()
			if t, err = e.cl.AddMagnet(magnet); err != nil {
				e.log.Warn("restore torrent", "hash", r.InfoHash, "err", err)
				continue
			}
		}
		// Nothing moves until the selection is applied below.
		t.DisallowDataDownload()
		e.added(t)

		e.mu.Lock()
		en := e.track(t, r.Trackers)
		for _, i := range r.OnlyFiles {
			en.selected[i] = true
		}
		en.paused = r.Paused
		en.verifying = r.Verify
		e.mu.Unlock()
		go e.resume(en)
	}
	e.saveSession()

	// Ours now holds everything it had, metainfo included.
	if imported != "" && torrent.Within(e.opts.CacheDir, imported) {
		if _, err := os.Stat(sessionFile(e.opts.SessionDir)); err == nil {
			if err := os.RemoveAll(imported); err != nil {
				e.log.Warn("remove rqbit's session", "err", err)
			}
		}
	}
	return nil
}

// resume applies a restored torrent's selection once its info is known, after
// hashing data that came from rqbit.
func (e *Engine) resume(en *entry) {
	select {
	case <-en.t.GotInfo():
	case <-en.t.Closed():
		return
	}
	e.forgetMissing(en.t)
	e.mu.Lock()
	verify := en.verifying
	e.mu.Unlock()
	if verify {
		err := en.t.VerifyDataContext(context.Background())
		e.mu.Lock()
		en.verifying = false
		if err != nil && !errors.Is(err, context.Canceled) {
			en.err = "checking data on disk: " + err.Error()
		}
		e.mu.Unlock()
		e.log.Info("checked carried-over download", "hash", en.t.InfoHash().HexString(), "done", en.t.BytesCompleted())
		e.saveSession()
	}
	e.apply(en)
}

func readSession(dir string) (sessionDoc, error) {
	var doc sessionDoc
	body, err := os.ReadFile(sessionFile(dir))
	if err != nil {
		return doc, err
	}
	return doc, json.Unmarshal(body, &doc)
}

// importLegacy reads the first rqbit session found: its torrents in the cache,
// their selection and metainfo. It returns the session's directory.
func (e *Engine) importLegacy() (sessionDoc, string, error) {
	var out sessionDoc
	for _, dir := range e.opts.LegacySessions {
		if dir == "" {
			continue
		}
		body, err := os.ReadFile(sessionFile(dir))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, "", err
		}
		var legacy struct {
			Torrents map[string]record `json:"torrents"`
		}
		if err := json.Unmarshal(body, &legacy); err != nil {
			return out, "", err
		}
		for _, r := range legacy.Torrents {
			if r.InfoHash == "" || !torrent.Within(e.opts.CacheDir, r.OutputFolder) {
				continue
			}
			name := strings.ToLower(r.InfoHash) + ".torrent"
			if mi, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				if err := writeAtomic(filepath.Join(e.opts.SessionDir, name), mi); err != nil {
					return out, "", err
				}
			}
			r.Verify = true
			out.Torrents = append(out.Torrents, r)
		}
		slices.SortFunc(out.Torrents, func(a, b record) int { return strings.Compare(a.InfoHash, b.InfoHash) })
		return out, dir, nil
	}
	return out, "", nil
}
