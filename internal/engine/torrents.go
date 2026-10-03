package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"kuro/internal/torrent"
)

// hashOf reads the info hash from a magnet link.
func hashOf(magnet string) (metainfo.Hash, []string, error) {
	m, err := metainfo.ParseMagnetUri(strings.TrimSpace(magnet))
	if err != nil {
		return metainfo.Hash{}, nil, err
	}
	return m.InfoHash, m.Trackers, nil
}

func (e *Engine) awaitInfo(ctx context.Context, t *atorrent.Torrent) error {
	select {
	case <-t.GotInfo():
		return nil
	case <-ctx.Done():
		return fmt.Errorf("metadata: %w", ctx.Err())
	}
}

// inspect resolves a magnet's file list without downloading any of it.
func (e *Engine) inspect(ctx context.Context, magnet string) (torrent.Detail, error) {
	hash, _, err := hashOf(magnet)
	if err != nil {
		return torrent.Detail{}, err
	}
	e.mu.Lock()
	if en, ok := e.byHash[hash]; ok {
		e.mu.Unlock()
		if err := e.awaitInfo(ctx, en.t); err != nil {
			return torrent.Detail{}, err
		}
		return e.detail(en), nil
	}
	in, ok := e.inspected[hash]
	if !ok {
		t, err := e.cl.AddMagnet(magnet)
		if err != nil {
			e.mu.Unlock()
			return torrent.Detail{}, err
		}
		t.DisallowDataDownload()
		e.added(t)
		in = &inspection{t: t}
		e.inspected[hash] = in
	}
	in.expires = time.Now().Add(inspectionTTL)
	e.mu.Unlock()

	if err := e.awaitInfo(ctx, in.t); err != nil {
		return torrent.Detail{}, err
	}
	// An add may have taken the torrent over meanwhile; its selection stands.
	e.mu.Lock()
	en, managed := e.byHash[hash]
	if !managed {
		for _, f := range in.t.Files() {
			f.SetPriority(atorrent.PiecePriorityNone)
		}
	}
	e.mu.Unlock()
	if managed {
		return e.detail(en), nil
	}
	return detailOf(in.t, e.opts.CacheDir, nil), nil
}

// add starts a torrent, or adds files to one already held: either way the
// files matching only are selected and the rest kept as they were.
func (e *Engine) add(ctx context.Context, magnet, only string) (*entry, error) {
	hash, trackers, err := hashOf(magnet)
	if err != nil {
		return nil, err
	}
	var pattern *regexp.Regexp
	if only != "" {
		if pattern, err = regexp.Compile(only); err != nil {
			return nil, fmt.Errorf("only_files_regex: %w", err)
		}
	}

	e.mu.Lock()
	en, ok := e.byHash[hash]
	if !ok {
		var t *atorrent.Torrent
		if in, inspected := e.inspected[hash]; inspected {
			t = in.t
			delete(e.inspected, hash)
		} else if t, err = e.cl.AddMagnet(magnet); err != nil {
			e.mu.Unlock()
			return nil, err
		} else {
			e.added(t)
		}
		en = e.track(t, trackers)
	}
	e.mu.Unlock()

	if err := e.awaitInfo(ctx, en.t); err != nil {
		return nil, err
	}
	if !ok {
		e.forgetMissing(en.t)
	}
	e.mu.Lock()
	for i, f := range en.t.Files() {
		if pattern == nil || pattern.MatchString(fileName(en.t, f)) {
			en.selected[i] = true
		}
	}
	e.mu.Unlock()
	e.apply(en)
	e.saveSession()
	return en, nil
}

// forgetMissing clears completion records for pieces whose file is gone (the
// cache or the user deleted it), or later writes extending the file would
// make those records look true over zero-filled holes.
func (e *Engine) forgetMissing(t *atorrent.Torrent) {
	for _, f := range t.Files() {
		if _, err := os.Stat(filePath(e.opts.CacheDir, t, f)); err == nil {
			continue
		}
		for i := f.BeginPieceIndex(); i < f.EndPieceIndex(); i++ {
			e.pieces.Set(metainfo.PieceKey{InfoHash: t.InfoHash(), Index: i}, false)
		}
	}
}

// track registers a torrent under a new id. Caller holds the lock.
func (e *Engine) track(t *atorrent.Torrent, trackers []string) *entry {
	en := &entry{id: e.nextID, t: t, trackers: trackers, selected: map[int]bool{}}
	e.nextID++
	e.byID[en.id] = en
	e.byHash[t.InfoHash()] = en
	if len(trackers) > 0 {
		t.AddTrackers([][]string{trackers})
	}
	return en
}

// apply turns the selection and pause state into file priorities.
func (e *Engine) apply(en *entry) {
	if en.t.Info() == nil {
		return
	}
	e.mu.Lock()
	selected := maps(en.selected)
	paused := en.paused
	e.mu.Unlock()
	for i, f := range en.t.Files() {
		if selected[i] {
			f.SetPriority(atorrent.PiecePriorityNormal)
		} else {
			f.SetPriority(atorrent.PiecePriorityNone)
		}
	}
	if paused {
		en.t.DisallowDataDownload()
	} else {
		en.t.AllowDataDownload()
	}
}

func maps(m map[int]bool) map[int]bool {
	out := make(map[int]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (e *Engine) setPaused(en *entry, paused bool) {
	e.mu.Lock()
	en.paused = paused
	e.mu.Unlock()
	e.apply(en)
	e.saveSession()
}

// remove drops a torrent; with data, its files go too.
func (e *Engine) remove(en *entry, data bool) error {
	e.mu.Lock()
	delete(e.byID, en.id)
	delete(e.byHash, en.t.InfoHash())
	e.mu.Unlock()

	var paths []string
	var pieces int
	if info := en.t.Info(); info != nil && data {
		for _, f := range en.t.Files() {
			paths = append(paths, filePath(e.opts.CacheDir, en.t, f))
		}
		pieces = info.NumPieces()
	}
	hash := en.t.InfoHash()
	en.t.Drop()
	<-en.t.Closed()

	os.Remove(filepath.Join(e.opts.SessionDir, hash.HexString()+".torrent"))
	e.saveSession()
	if !data {
		return nil
	}
	// Completion would otherwise claim pieces whose bytes are gone.
	for i := range pieces {
		e.pieces.Set(metainfo.PieceKey{InfoHash: hash, Index: i}, false)
	}
	var failed []string
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			failed = append(failed, p)
		}
	}
	if folder := outputFolder(e.opts.CacheDir, en.t); folder != filepath.Clean(e.opts.CacheDir) {
		removeEmptyDirs(folder)
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not remove %s", strings.Join(failed, ", "))
	}
	return nil
}

// removeEmptyDirs deletes dir and empty folders below it, leaving any with files.
func removeEmptyDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, d := range entries {
		if d.IsDir() {
			removeEmptyDirs(filepath.Join(dir, d.Name()))
		}
	}
	os.Remove(dir)
}

// fileName is a file's path inside the torrent, "/"-joined.
func fileName(t *atorrent.Torrent, f *atorrent.File) string {
	parts := components(t, f)
	return strings.Join(parts, "/")
}

func components(t *atorrent.Torrent, f *atorrent.File) []string {
	info := t.Info()
	if !info.IsDir() {
		return []string{info.BestName()}
	}
	fi := f.FileInfo()
	return slices.Clone(fi.BestPath())
}

// outputFolder is where a torrent's files go: the cache for one file, a folder
// named after the torrent for several.
func outputFolder(cacheDir string, t *atorrent.Torrent) string {
	info := t.Info()
	if info == nil || !info.IsDir() {
		return filepath.Clean(cacheDir)
	}
	return filepath.Join(cacheDir, info.BestName())
}

func filePath(cacheDir string, t *atorrent.Torrent, f *atorrent.File) string {
	return filepath.Join(append([]string{outputFolder(cacheDir, t)}, components(t, f)...)...)
}

func (e *Engine) detail(en *entry) torrent.Detail {
	e.mu.Lock()
	selected := maps(en.selected)
	e.mu.Unlock()
	return detailOf(en.t, e.opts.CacheDir, selected)
}

func detailOf(t *atorrent.Torrent, cacheDir string, selected map[int]bool) torrent.Detail {
	d := torrent.Detail{InfoHash: t.InfoHash().HexString(), Name: t.Name(), OutputFolder: outputFolder(cacheDir, t)}
	if t.Info() == nil {
		return d
	}
	for i, f := range t.Files() {
		d.Files = append(d.Files, torrent.File{
			Name:       fileName(t, f),
			Components: components(t, f),
			Length:     f.Length(),
			Included:   selected[i],
		})
	}
	return d
}

// stats reports the fields kuro reads, in rqbit's stats/v1 shape.
func (e *Engine) stats(en *entry) torrent.Stats {
	var s torrent.Stats
	e.mu.Lock()
	selected := maps(en.selected)
	paused, verifying, failure := en.paused, en.verifying, en.err
	e.mu.Unlock()

	s.Error = failure
	switch {
	case en.t.Info() == nil || verifying:
		s.State = "initializing"
	case paused:
		s.State = "paused"
	default:
		s.State = "live"
	}
	if en.t.Info() != nil {
		for i, f := range en.t.Files() {
			done := f.BytesCompleted()
			s.FileProgress = append(s.FileProgress, done)
			if selected[i] {
				s.ProgressBytes += done
				s.TotalBytes += f.Length()
			}
		}
		s.Finished = s.TotalBytes > 0 && s.ProgressBytes >= s.TotalBytes
	}

	ts := en.t.Stats()
	fetched := ts.BytesReadUsefulData.Int64()
	now := time.Now()
	e.mu.Lock()
	if !en.lastAt.IsZero() {
		if dt := now.Sub(en.lastAt).Seconds(); dt >= 1 {
			en.mibps = float64(fetched-en.lastBytes) / (1 << 20) / dt
			en.lastBytes, en.lastAt = fetched, now
		}
	} else {
		en.lastBytes, en.lastAt = fetched, now
	}
	speed := en.mibps
	e.mu.Unlock()

	if s.State == "live" {
		live := &torrent.Live{}
		live.DownloadSpeed.Mbps = speed
		live.Snapshot.FetchedBytes = ts.BytesReadData.Int64()
		live.Snapshot.PeerStats.Live = ts.ActivePeers
		s.Live = live
	}
	return s
}
