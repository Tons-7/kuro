package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kuro/internal/store"
	"kuro/internal/torrent"
)

// Cache keeps what you have watched on disk within a size budget, evicting
// whole files oldest-first.
type Cache struct {
	store   *store.Store
	torrent *torrent.Client
	dir     string
	log     *slog.Logger
}

func NewCache(s *store.Store, tc *torrent.Client, dir string, log *slog.Logger) *Cache {
	return &Cache{store: s, torrent: tc, dir: dir, log: log}
}

type SweepReport struct {
	Before  int64 `json:"before"`
	After   int64 `json:"after"`
	Budget  int64 `json:"budget"`
	Evicted int   `json:"evicted"`
	Freed   int64 `json:"freed"`
}

// Sweep brings usage under budget: unprotected files oldest-first, then
// episodes of shows still in progress. Pinned and kept files are never evicted
// and kept ones are not in the usage either.
func (c *Cache) Sweep(ctx context.Context) (SweepReport, error) {
	if err := c.refresh(ctx); err != nil {
		c.log.Warn("refresh cache sizes", "err", err)
	}

	usage, err := c.store.CacheUsage(ctx)
	if err != nil {
		return SweepReport{}, err
	}

	rep := SweepReport{Before: usage.Bytes, After: usage.Bytes, Budget: usage.Budget}
	if usage.Budget <= 0 || usage.Bytes <= usage.Budget {
		return rep, nil
	}

	// Eviction takes the whole torrent, so count each hash once.
	freed := map[string]int64{}
	for _, e := range usage.Entries {
		freed[e.InfoHash] += e.Bytes
	}

	gone := map[string]bool{}
	for _, e := range evictionOrder(usage.Entries) {
		if rep.After <= usage.Budget {
			break
		}
		if gone[e.InfoHash] {
			continue
		}
		if err := c.evict(ctx, e); err != nil {
			c.log.Warn("evict", "hash", e.InfoHash, "err", err)
			continue
		}
		gone[e.InfoHash] = true
		rep.Evicted++
		rep.Freed += freed[e.InfoHash]
		rep.After -= freed[e.InfoHash]
	}

	if rep.Evicted > 0 {
		c.log.Info("cache swept",
			"evicted", rep.Evicted,
			"freedMB", rep.Freed>>20,
			"usedMB", rep.After>>20,
			"budgetMB", usage.Budget>>20)
	}
	return rep, nil
}

// A pin or a keep covers its whole torrent: eviction deletes every file of it.
func evictionOrder(entries []store.CacheEntry) []store.CacheEntry {
	held := map[string]bool{}
	for _, e := range entries {
		if e.Pinned || e.Kept {
			held[e.InfoHash] = true
		}
	}

	var plain, protected []store.CacheEntry
	for _, e := range entries {
		switch {
		case held[e.InfoHash]:
			continue
		case e.Protected:
			protected = append(protected, e)
		default:
			plain = append(plain, e)
		}
	}

	byAge := func(list []store.CacheEntry) {
		for i := 1; i < len(list); i++ {
			for j := i; j > 0 && list[j].LastPlayed < list[j-1].LastPlayed; j-- {
				list[j], list[j-1] = list[j-1], list[j]
			}
		}
	}
	byAge(plain)
	byAge(protected)

	return append(plain, protected...)
}

// AutoDelete removes watched episodes of one show by the cache.autodelete rule:
// "now" once watched, "keep2" once the two after it are watched too. Kept
// downloads stay unless cache.autodelete_downloads says otherwise; pinned files
// are playing; a torrent goes only when every file in it qualifies. Library
// files are not cache entries and are never touched.
func (c *Cache) AutoDelete(ctx context.Context, animeID int) (int, error) {
	if animeID == 0 {
		return 0, nil
	}
	prefs, err := c.store.Prefs(ctx, animeID)
	if err != nil {
		return 0, err
	}
	mode := prefs.String("cache.autodelete")
	if mode != "now" && mode != "keep2" {
		return 0, nil
	}
	downloads := prefs.Bool("cache.autodelete_downloads")

	watched, err := c.store.WatchedEpisodes(ctx, animeID)
	if err != nil {
		return 0, err
	}
	entries, err := c.store.CacheEntries(ctx)
	if err != nil {
		return 0, err
	}

	done := func(ep int) bool {
		return watched[ep] && (mode == "now" || (watched[ep+1] && watched[ep+2]))
	}
	veto := map[string]bool{}
	candidates := map[string]bool{}
	for _, e := range entries {
		if e.AnimeID == nil || *e.AnimeID != animeID {
			veto[e.InfoHash] = true
			continue
		}
		ep, err := strconv.Atoi(e.EpKey)
		if err != nil || e.Pinned || (e.Kept && !downloads) || !done(ep) {
			veto[e.InfoHash] = true
			continue
		}
		candidates[e.InfoHash] = true
	}

	removed := 0
	for hash := range candidates {
		if veto[hash] {
			continue
		}
		if err := c.evict(ctx, store.CacheEntry{InfoHash: hash, RqbitID: c.rqbitID(ctx, hash)}); err != nil {
			c.log.Warn("auto-delete watched", "hash", hash, "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		c.log.Info("watched episodes removed", "anime", animeID, "count", removed, "rule", mode)
	}
	return removed, nil
}

// dropSuperseded removes releases another one replaced for an episode. Hidden
// from Downloads once deselected, they would otherwise sit in the engine — for
// good, if kept — costing their full length.
func dropSuperseded(ctx context.Context, st *store.Store, tc *torrent.Client, log *slog.Logger, animeID int, epKey string) {
	hashes, err := st.Superseded(ctx, animeID, epKey)
	if err != nil || len(hashes) == 0 {
		return
	}
	live := map[string]int{}
	if tc != nil {
		if l, err := tc.Live(ctx); err == nil {
			live = l
		}
	}
	for _, hash := range hashes {
		if id, ok := live[strings.ToLower(hash)]; ok {
			if err := tc.Delete(ctx, id); err != nil {
				log.Warn("remove superseded release", "hash", hash, "err", err)
				continue
			}
		}
		if err := st.DropTorrentCache(ctx, hash); err != nil {
			log.Warn("forget superseded release", "hash", hash, "err", err)
			continue
		}
		log.Info("superseded release removed", "anime", animeID, "episode", epKey, "hash", hash)
	}
}

// Keep moves one download into or out of the kept tier.
func (c *Cache) Keep(ctx context.Context, infoHash string, kept bool) error {
	found, err := c.store.KeepDownload(ctx, infoHash, kept)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no download for %s", infoHash)
	}
	return nil
}

// Remove deletes one download and its data. A pinned entry is playing right
// now, so removing it would pull the file out from under the viewer.
func (c *Cache) Remove(ctx context.Context, infoHash string) (int64, error) {
	entries, err := c.store.CacheEntries(ctx)
	if err != nil {
		return 0, err
	}

	var freed int64
	var found bool
	for _, e := range entries {
		if !strings.EqualFold(e.InfoHash, infoHash) {
			continue
		}
		if e.Pinned {
			return 0, fmt.Errorf("that episode is playing right now")
		}
		freed += e.Bytes
		found = true
	}
	if !found {
		// Known to the engine but never tracked, which adoption normally fixes.
		if id, ok := c.liveID(ctx, infoHash); ok {
			return 0, c.torrent.Delete(ctx, id)
		}
		return 0, fmt.Errorf("no download for %s", infoHash)
	}

	return freed, c.evict(ctx, store.CacheEntry{
		InfoHash: infoHash,
		RqbitID:  c.rqbitID(ctx, infoHash),
	})
}

// Clear removes every download that is not playing. completedOnly keeps the
// ones still fetching.
func (c *Cache) Clear(ctx context.Context, completedOnly bool) (int, int64, error) {
	entries, err := c.store.CacheEntries(ctx)
	if err != nil {
		return 0, 0, err
	}

	// Evicting deletes the whole torrent, so one pinned file protects every
	// other file sharing its hash.
	held := map[string]bool{}
	for _, e := range entries {
		if e.Pinned {
			held[strings.ToLower(e.InfoHash)] = true
		}
	}

	var removed int
	var freed int64
	done := map[string]bool{}

	for _, e := range entries {
		hash := strings.ToLower(e.InfoHash)
		if held[hash] || done[hash] {
			continue
		}
		if completedOnly && !e.Complete {
			continue
		}
		if err := c.evict(ctx, e); err != nil {
			c.log.Warn("clear download", "hash", hash, "err", err)
			continue
		}
		done[hash] = true
		removed++
		freed += e.Bytes
	}
	return removed, freed, nil
}

func (c *Cache) liveID(ctx context.Context, infoHash string) (int, bool) {
	if c.torrent == nil {
		return 0, false
	}
	live, err := c.torrent.List(ctx)
	if err != nil {
		return 0, false
	}
	for _, t := range live.Torrents {
		if strings.EqualFold(t.InfoHash, infoHash) {
			return t.ID, true
		}
	}
	return 0, false
}

func (c *Cache) rqbitID(ctx context.Context, infoHash string) *int {
	if id, ok := c.liveID(ctx, infoHash); ok {
		return &id
	}
	return nil
}

// Progress is what has been fetched, not what the file costs on disk: rqbit
// reserves the full length up front.
type Progress struct {
	Bytes    int64 `json:"bytes"`
	Total    int64 `json:"total"`
	Finished bool  `json:"finished"`
	Paused   bool  `json:"paused"`
	// Checking: rqbit verifying the file after a launch, neither paused nor downloading.
	Checking bool `json:"checking"`
	// Mbps and Peers are what tell a slow download from a stalled one.
	Mbps  float64 `json:"mbps"`
	Peers int     `json:"peers"`
}

// Pause stops fetching without discarding what is already on disk. Resume
// starts it again.
func (c *Cache) Pause(ctx context.Context, infoHash string) error {
	return c.setRunning(ctx, infoHash, false)
}

func (c *Cache) Resume(ctx context.Context, infoHash string) error {
	return c.setRunning(ctx, infoHash, true)
}

func (c *Cache) setRunning(ctx context.Context, infoHash string, run bool) error {
	if c.torrent == nil {
		return fmt.Errorf("torrent engine unavailable")
	}

	live, err := c.torrent.Live(ctx)
	if err != nil {
		return err
	}
	id, ok := live[strings.ToLower(infoHash)]
	if !ok {
		return fmt.Errorf("not held by the engine")
	}

	if run {
		return c.torrent.Start(ctx, id)
	}
	return c.torrent.Pause(ctx, id)
}

// Finished reports whether the engine has every byte of a torrent.
// Swarm is what the peers are sending for a torrent right now.
func (c *Cache) Swarm(ctx context.Context, infoHash string) (mbps float64, peers int) {
	if c.torrent == nil {
		return 0, 0
	}
	id, ok := c.liveID(ctx, infoHash)
	if !ok {
		return 0, 0
	}
	stats, err := c.torrent.Stats(ctx, id)
	if err != nil || stats.Live == nil {
		return 0, 0
	}
	return stats.Live.DownloadSpeed.Mbps, stats.Live.Snapshot.PeerStats.Live
}

func (c *Cache) Finished(ctx context.Context, infoHash string) bool {
	if c.torrent == nil {
		return false
	}
	id, ok := c.liveID(ctx, infoHash)
	if !ok {
		return false
	}
	stats, err := c.torrent.Stats(ctx, id)
	return err == nil && stats.Finished
}

// Downloaded reports how much of a torrent is really there. File size says
// nothing: an untouched torrent still has a full-size file of holes.
func (c *Cache) Downloaded(ctx context.Context, infoHash string) int64 {
	if c.torrent == nil {
		return 0
	}
	live, err := c.torrent.Live(ctx)
	if err != nil {
		return 0
	}
	id, ok := live[strings.ToLower(infoHash)]
	if !ok {
		return 0
	}
	stats, err := c.torrent.Stats(ctx, id)
	if err != nil {
		return 0
	}
	return stats.ProgressBytes
}

func (c *Cache) Progress(ctx context.Context) (map[string]Progress, error) {
	if c.torrent == nil {
		return nil, nil
	}
	live, err := c.torrent.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[string]Progress, len(live.Torrents))
	for _, t := range live.Torrents {
		stats, err := c.torrent.Stats(ctx, t.ID)
		if err != nil {
			continue
		}
		p := Progress{
			Bytes:    stats.ProgressBytes,
			Total:    stats.TotalBytes,
			Finished: stats.Finished,
			Checking: stats.Checking(),
			// No live section means the engine is not working on it.
			Paused: stats.Live == nil && !stats.Finished && !stats.Checking(),
		}
		if stats.Live != nil {
			p.Mbps = stats.Live.DownloadSpeed.Mbps
			p.Peers = stats.Live.Snapshot.PeerStats.Live
		}
		out[strings.ToLower(t.InfoHash)] = p
	}
	return out, nil
}

// A download removed outside kuro leaves a row still charging the budget for
// bytes that are gone, and nothing else ever clears it.
func (c *Cache) forgetVanished(ctx context.Context) error {
	live, err := c.torrent.List(ctx)
	if err != nil {
		return err
	}

	held := make(map[string]struct{}, len(live.Torrents))
	for _, t := range live.Torrents {
		held[strings.ToLower(t.InfoHash)] = struct{}{}
	}

	entries, err := c.store.CacheEntries(ctx)
	if err != nil {
		return err
	}

	dropped := map[string]bool{}
	for _, e := range entries {
		hash := strings.ToLower(e.InfoHash)
		if _, ok := held[hash]; ok || dropped[hash] {
			continue
		}
		if err := c.store.DropTorrentCache(ctx, e.InfoHash); err != nil {
			return err
		}
		dropped[hash] = true
	}
	if len(dropped) > 0 {
		c.log.Info("forgot downloads the engine no longer holds", "count", len(dropped))
	}
	return nil
}

func (c *Cache) evict(ctx context.Context, e store.CacheEntry) error {
	if c.torrent != nil && e.RqbitID != nil {
		// Delete removes the data; forgetting would leave the files behind.
		// With no engine to delete through, the record has to stay too.
		if err := c.torrent.Delete(ctx, *e.RqbitID); err != nil {
			if errors.Is(err, torrent.ErrUnavailable) {
				return err
			}
			c.log.Warn("delete torrent", "id", *e.RqbitID, "err", err)
		}
	}
	return c.store.DropTorrentCache(ctx, e.InfoHash)
}

// Sizes come from allocation, not progress: rqbit reserves each file's full
// length up front, so the space is committed before the bytes arrive.
func (c *Cache) refresh(ctx context.Context) error {
	if c.torrent == nil {
		return nil
	}
	if err := c.adopt(ctx); err != nil {
		c.log.Warn("adopt untracked torrents", "err", err)
	}
	if err := c.forgetVanished(ctx); err != nil {
		c.log.Warn("forget vanished downloads", "err", err)
	}

	entries, err := c.store.CacheEntries(ctx)
	if err != nil {
		return err
	}

	// Entries are per file, so a season pack would otherwise ask the engine for
	// the same torrent once per episode, twice over.
	seenStats := map[int]torrent.Stats{}
	seenDetail := map[int]torrent.Detail{}

	for _, e := range entries {
		if e.RqbitID == nil {
			continue
		}
		id := *e.RqbitID
		stats, known := seenStats[id]
		if !known {
			s, err := c.torrent.Stats(ctx, id)
			if err != nil {
				continue
			}
			stats, seenStats[id] = s, s
		}

		detail, known := seenDetail[id]
		if !known {
			d, err := c.torrent.Details(ctx, id)
			if err != nil {
				c.log.Warn("measure download", "id", id, "err", err)
			}
			detail, seenDetail[id] = d, d
		}

		bytes := stats.ProgressBytes
		if held := heldBytes(detail, e.FileIndex); held > bytes {
			bytes = held
		}

		if err := c.store.SetCacheBytes(ctx, e.InfoHash, e.FileIndex,
			bytes, stats.Finished); err != nil {
			return err
		}
	}
	return nil
}

// heldBytes is what the torrent's files actually occupy on disk, which is what
// the budget spends; progress alone misses a file rqbit has fully allocated.
func heldBytes(detail torrent.Detail, fileIndex int) int64 {
	var total int64
	for i, f := range detail.Files {
		if !f.Included {
			continue
		}
		if fileIndex != store.WholeTorrent && i != fileIndex {
			continue
		}

		path := filepath.Join(detail.OutputFolder, filepath.Join(f.Components...))
		if len(f.Components) == 0 {
			path = filepath.Join(detail.OutputFolder, f.Name)
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		total += allocatedSize(path, info)
	}
	return total
}

// adopt gives every torrent the engine holds a cache row, so downloads pulled
// outside playback count against the budget instead of being invisible to it.
func (c *Cache) adopt(ctx context.Context) error {
	live, err := c.torrent.List(ctx)
	if err != nil {
		return err
	}

	known, err := c.store.CacheEntries(ctx)
	if err != nil {
		return err
	}
	tracked := make(map[string]struct{}, len(known))
	for _, e := range known {
		tracked[strings.ToLower(e.InfoHash)] = struct{}{}
	}

	var added int
	for _, t := range live.Torrents {
		hash := strings.ToLower(t.InfoHash)
		if hash == "" {
			continue
		}
		if _, ok := tracked[hash]; ok {
			continue
		}
		if err := c.store.TrackTorrent(ctx, hash, t.ID, t.Name); err != nil {
			c.log.Warn("track torrent", "hash", hash, "err", err)
			continue
		}
		added++
	}
	if added > 0 {
		c.log.Info("adopted untracked downloads", "count", added)
	}
	return nil
}

// Not downloads: transcode output, scrub sheets and a staged update all live
// in the cache directory too.
var ours = map[string]bool{"hls": true, "thumbs": true, "update": true}

// Orphans are cache-directory files belonging to no torrent the engine knows
// about; nothing else deletes them and they escape the budget.
func (c *Cache) Orphans(ctx context.Context) (files int, bytes int64, err error) {
	dir := c.dir
	live, err := c.torrent.List(ctx)
	if err != nil {
		return 0, 0, err
	}

	keep := map[string]struct{}{}
	for _, t := range live.Torrents {
		if t.Name != "" {
			keep[strings.ToLower(t.Name)] = struct{}{}
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}

	for _, e := range entries {
		name := e.Name()
		// The cache directory is shared: these belong to other parts of the app
		// and each has its own lifecycle.
		if ours[name] || strings.HasPrefix(name, "chapters-") {
			continue
		}
		if _, wanted := keep[strings.ToLower(name)]; wanted {
			continue
		}

		path := filepath.Join(dir, name)
		size := dirSize(path)
		if err := os.RemoveAll(path); err != nil {
			c.log.Warn("remove orphaned download", "path", name, "err", err)
			continue
		}
		files++
		bytes += size
	}

	if files > 0 {
		c.log.Info("removed orphaned downloads", "files", files, "freedMB", bytes>>20)
	}
	return files, bytes, nil
}

func dirSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return allocatedSize(path, info)
	}

	var total int64
	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += allocatedSize(p, fi)
		}
		return nil
	})
	return total
}

// Run sweeps periodically. Downloads grow between plays, so the budget has to
// be enforced continuously rather than only when playback starts.
func (c *Cache) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 2 * time.Minute
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		if _, err := c.Sweep(ctx); err != nil {
			c.log.Warn("cache sweep", "err", err)
		}
	}
}
