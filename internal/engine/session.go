package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"kuro/internal/bt"
	"kuro/internal/bt/metainfo"

	"kuro/internal/torrent"
)

// record is one torrent in session.json, with rqbit's field names so its session reads too.
type record struct {
	InfoHash     string   `json:"info_hash"`
	Trackers     []string `json:"trackers,omitempty"`
	OutputFolder string   `json:"output_folder,omitempty"`
	OnlyFiles    []int    `json:"only_files"`
	Paused       bool     `json:"is_paused"`
	// Verify: data came from elsewhere (rqbit), so completion is unknown until hashed.
	Verify bool `json:"verify,omitempty"`
	// Top is the file or folder name the data lives under, when not the torrent's own.
	Top string `json:"top,omitempty"`
}

type sessionDoc struct {
	Torrents []record `json:"torrents"`
}

// saveSession writes the torrents and missing metainfo, so a restart needs nothing from the swarm.
func (e *Engine) saveSession() { e.writeSession(false) }

// writeSession is saveSession; once Stop has written the last one (final), a late save is dropped.
func (e *Engine) writeSession(final bool) {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	if e.cl == nil || (e.closed && !final) {
		e.mu.Unlock()
		return
	}
	var doc sessionDoc
	var withInfo []*bt.Torrent
	for _, en := range e.byID {
		r := record{InfoHash: en.t.InfoHash().HexString(), Trackers: en.trackers, Paused: en.paused, Verify: en.verifying}
		if info := en.t.Info(); info != nil {
			if top := e.names.top(info.BestName(), en.t.InfoHash()); top != info.BestName() {
				r.Top = top
			}
		}
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

// writeAtomic lands the whole file or none: synced before the rename, so a crash cannot truncate the session.
func writeAtomic(path string, body []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// restore re-adds the saved torrents; on first run it imports rqbit's.
func (e *Engine) restore() error {
	var imported string
	doc, err := readSession(e.opts.SessionDir)
	var broken *json.SyntaxError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc, imported, err = e.importLegacy()
		if err != nil {
			return err
		}
		if imported != "" {
			e.log.Info("carried over downloads from rqbit", "torrents", len(doc.Torrents), "from", imported)
		}
	case errors.As(err, &broken) || errors.Is(err, io.ErrUnexpectedEOF):
		// A damaged list: every torrent whose metainfo is here comes back paused, nothing is dropped.
		doc = e.rebuildSession()
		e.log.Warn("torrent session unreadable; rebuilt from the saved torrents", "err", err, "torrents", len(doc.Torrents))
	case err != nil:
		return err
	}

	for _, r := range doc.Torrents {
		var hash metainfo.Hash
		if err := hash.FromHexString(strings.ToLower(r.InfoHash)); err != nil {
			e.log.Warn("restore torrent: bad info hash", "hash", r.InfoHash, "err", err)
			continue
		}
		e.names.claim(hash, r.Top)
		var t *bt.Torrent
		path := filepath.Join(e.opts.SessionDir, hash.HexString()+".torrent")
		if mi, err := metainfo.LoadFromFile(path); err == nil {
			spec, err := bt.TorrentSpecFromMetaInfoErr(mi)
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

// resume applies a restored torrent's selection once its info is known and rqbit data hashed.
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

// rebuildSession lists the torrents whose metainfo is on disk, all paused, and moves the unreadable list aside.
func (e *Engine) rebuildSession() sessionDoc {
	var doc sessionDoc
	path := sessionFile(e.opts.SessionDir)
	os.Rename(path, path+".bad")
	entries, _ := os.ReadDir(e.opts.SessionDir)
	for _, d := range entries {
		name := d.Name()
		if !strings.HasSuffix(name, ".torrent") {
			continue
		}
		mi, err := metainfo.LoadFromFile(filepath.Join(e.opts.SessionDir, name))
		if err != nil {
			continue
		}
		info, err := mi.UnmarshalInfo()
		if err != nil {
			continue
		}
		r := record{InfoHash: mi.HashInfoBytes().HexString(), Paused: true}
		for i := range info.UpvertedFiles() {
			r.OnlyFiles = append(r.OnlyFiles, i)
		}
		doc.Torrents = append(doc.Torrents, r)
	}
	return doc
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
