package engine

import (
	"path/filepath"
	"sync"

	"kuro/internal/bt"
	"kuro/internal/bt/metainfo"
)

// layout is where each torrent's files live: under its name, or with its hash appended when another
// torrent holds that name, so two same-named releases never write to or delete each other's files.
type layout struct {
	base string
	mu   sync.Mutex
	tops map[metainfo.Hash]string
}

func newLayout(base string) *layout {
	return &layout{base: base, tops: map[metainfo.Hash]string{}}
}

// top is the name a torrent's file or folder goes under, decided on first sight and kept after.
func (l *layout) top(name string, hash metainfo.Hash) string {
	if l == nil {
		return name
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if top, ok := l.tops[hash]; ok {
		return top
	}
	for other, top := range l.tops {
		if top == name && other != hash {
			name += " [" + hash.HexString()[:8] + "]"
			break
		}
	}
	l.tops[hash] = name
	return name
}

// claim records a name restored from the session, so a restart keeps every torrent where it was.
func (l *layout) claim(hash metainfo.Hash, top string) {
	if l == nil || top == "" {
		return
	}
	l.mu.Lock()
	l.tops[hash] = top
	l.mu.Unlock()
}

func (l *layout) forget(hash metainfo.Hash) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.tops, hash)
	l.mu.Unlock()
}

// folder is where a torrent's files go: the cache for one file, a folder named after the torrent for several.
func (l *layout) folder(t *bt.Torrent) string {
	info := t.Info()
	if info == nil || !info.IsDir() {
		return filepath.Clean(l.base)
	}
	return filepath.Join(l.base, l.top(info.BestName(), t.InfoHash()))
}

func (l *layout) filePath(t *bt.Torrent, f *bt.File) string {
	info := t.Info()
	if !info.IsDir() {
		return filepath.Join(l.base, l.top(info.BestName(), t.InfoHash()))
	}
	return filepath.Join(append([]string{l.folder(t)}, components(t, f)...)...)
}

// shared reports a path another held torrent also maps to, which a delete must leave alone.
func (l *layout) shared(path string, hash metainfo.Hash, others []*bt.Torrent) bool {
	for _, o := range others {
		if o.InfoHash() == hash || o.Info() == nil {
			continue
		}
		for _, f := range o.Files() {
			if l.filePath(o, f) == path {
				return true
			}
		}
	}
	return false
}
