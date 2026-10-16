package library

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"kuro/internal/mal"
	"kuro/internal/store"
)

const malTracker = "mal"

// MALAuthErrorSetting is set when MyAnimeList refuses the stored token, cleared by a good sync or a new login.
const MALAuthErrorSetting = "mal.auth_error"

var ErrMALReconnect = errors.New("myanimelist rejected the token; reconnect MyAnimeList in Settings")

// MALSync mirrors local progress to MyAnimeList. MAL is driven from local state,
// not synced against AniList, so the two never fight over which is newer.
type MALSync struct {
	store *store.Store
	mal   *mal.Client
	log   *slog.Logger
	// One of Run, PushOne and Pull at a time: a pull judging a site edit against a push in flight rolls it back.
	mu sync.Mutex
}

func NewMALSync(s *store.Store, c *mal.Client, log *slog.Logger) *MALSync {
	return &MALSync{store: s, mal: c, log: log}
}

type MALReport struct {
	Pushed  int `json:"pushed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Run pushes what MAL hasn't been told, stopping at an auth failure rather than repeating it.
func (m *MALSync) Run(ctx context.Context) (MALReport, error) {
	var rep MALReport
	if m.mal == nil || !m.mal.Authenticated() {
		return rep, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	pending, err := m.store.PendingMALPushes(ctx, malTracker, 200)
	if err != nil {
		return rep, err
	}

	for _, e := range pending {
		if mal.Status(e.Status) == "" && e.Progress == 0 {
			rep.Skipped++
			continue
		}

		if err := m.mal.SetProgress(ctx, e.RemoteID, e.Progress, e.Status, e.Repeat, e.Score, clears(e)); err != nil {
			rep.Failed++
			m.log.Warn("mal push", "anime", e.AnimeID, "mal", e.RemoteID, "err", err)
			if mal.Unauthorized(err) {
				return rep, m.refused(ctx)
			}
			if err := m.store.PushFailed(ctx, malTracker, e.AnimeID); err != nil {
				return rep, err
			}
			continue
		}
		if err := m.store.MarkPushed(ctx, malTracker, e.AnimeID, e.Progress, e.Status, e.Score); err != nil {
			return rep, err
		}
		rep.Pushed++
	}

	if rep.Pushed > 0 {
		m.log.Info("myanimelist updated", "entries", rep.Pushed)
		m.accepted(ctx)
	}
	return rep, nil
}

// PushOne updates a single anime when an episode finishes; no-op when nothing is new.
func (m *MALSync) PushOne(ctx context.Context, animeID int) error {
	if m.mal == nil || !m.mal.Authenticated() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.store.PendingMALPush(ctx, malTracker, animeID)
	if err != nil || e.AnimeID == 0 {
		return err
	}
	if err := m.mal.SetProgress(ctx, e.RemoteID, e.Progress, e.Status, e.Repeat, e.Score, clears(e)); err != nil {
		if mal.Unauthorized(err) {
			return m.refused(ctx)
		}
		return err
	}
	m.accepted(ctx)
	return m.store.MarkPushed(ctx, malTracker, e.AnimeID, e.Progress, e.Status, e.Score)
}

// refused records that MAL turned the token down, so Settings asks for a reconnect instead of saying Connected.
func (m *MALSync) refused(ctx context.Context) error {
	m.store.SetSetting(ctx, MALAuthErrorSetting, "true")
	return ErrMALReconnect
}

func (m *MALSync) accepted(ctx context.Context) {
	if v, _ := m.store.Setting(ctx, MALAuthErrorSetting); v != "" {
		m.store.SetSetting(ctx, MALAuthErrorSetting, "")
	}
}

// clears: MAL was told a score and the local one is now unrated.
func clears(e store.TrackerEntry) bool { return e.Score == 0 && e.LastScore > 0 }

type MALImport struct {
	Entries   int `json:"entries"`
	Matched   int `json:"matched"`
	Unmatched int `json:"unmatched"`
	// Applied is how many entries changed locally because MAL had moved on.
	Applied int `json:"applied"`
}

// Pull applies site edits, adds missing entries, and records the rest so connecting replays nothing.
func (m *MALSync) Pull(ctx context.Context) (MALImport, error) {
	var rep MALImport
	if m.mal == nil || !m.mal.Authenticated() {
		return rep, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := m.mal.List(ctx)
	if mal.Unauthorized(err) {
		return rep, m.refused(ctx)
	}
	if err != nil {
		return rep, err
	}
	m.accepted(ctx)
	rep.Entries = len(entries)

	malIDs := make([]int, 0, len(entries))
	for _, e := range entries {
		malIDs = append(malIDs, e.AnimeID)
	}
	mapping, err := m.store.AniListIDsForMAL(ctx, malIDs)
	if err != nil {
		return rep, err
	}

	var marks []store.TrackerEntry
	for _, e := range entries {
		animeID, ok := mapping[e.AnimeID]
		if !ok {
			rep.Unmatched++
			continue
		}
		rep.Matched++

		remote := store.TrackerEntry{
			AnimeID: animeID, RemoteID: e.AnimeID,
			Progress: e.Watched, Status: e.ListStatus(), Score: e.LocalScore(), Repeat: e.Rewatched,
		}
		last, seen, err := m.store.Pushed(ctx, malTracker, animeID)
		if err != nil {
			return rep, err
		}
		// MAL keeps whole points (85 is 9); unchanged in its units keeps our finer score.
		if seen && mal.MALScore(last.Score) == e.Score {
			remote.Score = last.Score
		}

		local, err := m.store.ListEntry(ctx, animeID)
		if err != nil {
			return rep, err
		}
		apply, ok := pulled(remote, local, last, seen, e)
		if !ok {
			// A local edit MAL has not been told about wins; MAL learns of it next run.
			continue
		}
		if apply != nil {
			apply.Since = local.Changed
			changed, err := m.store.ApplyRemote(ctx, *apply)
			if err != nil {
				return rep, err
			}
			if !changed {
				continue // edited locally while this ran
			}
			rep.Applied++
		}
		// Unchanged entries, most of a list, are skipped: rewrites compete with playback.
		if seen && last.Progress == remote.Progress && last.Status == remote.Status && last.Score == remote.Score {
			continue
		}
		marks = append(marks, remote)
	}
	if _, err := m.store.SeedPushed(ctx, malTracker, marks); err != nil {
		return rep, err
	}

	if rep.Applied > 0 {
		m.log.Info("myanimelist changes applied", "entries", rep.Applied)
	}
	return rep, nil
}

// pulled: MAL's changes to apply (nil: none), or false when an unpushed edit wins (not AniList's dirty flag).
func pulled(remote store.TrackerEntry, local store.DirtyEntry, last store.TrackerEntry, seen bool, e mal.Entry) (*store.RemoteEntry, bool) {
	site := store.RemoteEntry{AnimeID: remote.AnimeID, Status: remote.Status, Progress: remote.Progress, Score: remote.Score, Repeat: remote.Repeat}
	switch {
	case seen && agrees(last, remote.Progress, remote.Status, remote.Score):
		return nil, true // nothing new on MAL
	case local.AnimeID == 0:
		return &site, true
	case seen && !agrees(last, local.Progress, local.Status, local.Score):
		return nil, false // both moved since the last push
	case seen:
		return &site, true
	}

	// First sight of an entry both sides hold: the side edited last keeps progress and status, a lone score stays.
	merged := store.RemoteEntry{AnimeID: local.AnimeID, Status: local.Status, Progress: local.Progress,
		Score: local.Score, Repeat: max(local.Repeat, remote.Repeat)}
	if !e.Updated.IsZero() && e.Updated.Unix() > local.Changed {
		merged.Status, merged.Progress = remote.Status, remote.Progress
	}
	if merged.Score == 0 {
		merged.Score = remote.Score
	}
	if merged.Status == local.Status && merged.Progress == local.Progress && merged.Score == local.Score && merged.Repeat == local.Repeat {
		return nil, true
	}
	return &merged, true
}

func agrees(told store.TrackerEntry, progress int, status string, score int) bool {
	return told.Progress == progress && told.Status == status && told.Score == score
}

// Remove takes the show off the MAL list, under the push lock so a push in flight can't recreate it.
func (m *MALSync) Remove(ctx context.Context, animeID int) error {
	if m.mal == nil || !m.mal.Authenticated() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	malID, err := m.store.MalID(ctx, animeID)
	if err != nil || malID == 0 {
		return err
	}
	return m.mal.DeleteEntry(ctx, malID)
}
