package library

import (
	"context"
	"log/slog"

	"kuro/internal/mal"
	"kuro/internal/store"
)

const malTracker = "mal"

// MALSync mirrors local progress to MyAnimeList. MAL is driven from local state,
// not synced against AniList, so the two never fight over which is newer.
type MALSync struct {
	store *store.Store
	mal   *mal.Client
	log   *slog.Logger
}

func NewMALSync(s *store.Store, c *mal.Client, log *slog.Logger) *MALSync {
	return &MALSync{store: s, mal: c, log: log}
}

type MALReport struct {
	Pushed  int `json:"pushed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Run pushes everything MAL has not been told about. It stops on auth failure
// rather than repeating the same error down the list.
func (m *MALSync) Run(ctx context.Context) (MALReport, error) {
	var rep MALReport
	if m.mal == nil || !m.mal.Authenticated() {
		return rep, nil
	}

	pending, err := m.store.PendingMALPushes(ctx, malTracker, 200)
	if err != nil {
		return rep, err
	}

	for _, e := range pending {
		if mal.Status(e.Status) == "" && e.Progress == 0 {
			rep.Skipped++
			continue
		}

		if err := m.mal.SetProgress(ctx, e.RemoteID, e.Progress, e.Status, e.Repeat, e.Score); err != nil {
			rep.Failed++
			m.log.Warn("mal push", "anime", e.AnimeID, "mal", e.RemoteID, "err", err)
			if mal.Unauthorized(err) {
				break
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
	}
	return rep, nil
}

// PushOne updates a single anime when an episode finishes; no-op when nothing is new.
func (m *MALSync) PushOne(ctx context.Context, animeID int) error {
	if m.mal == nil || !m.mal.Authenticated() {
		return nil
	}

	e, err := m.store.PendingMALPush(ctx, malTracker, animeID)
	if err != nil || e.AnimeID == 0 {
		return err
	}
	if err := m.mal.SetProgress(ctx, e.RemoteID, e.Progress, e.Status, e.Repeat, e.Score); err != nil {
		return err
	}
	return m.store.MarkPushed(ctx, malTracker, e.AnimeID, e.Progress, e.Status, e.Score)
}

type MALImport struct {
	Entries   int `json:"entries"`
	Matched   int `json:"matched"`
	Unmatched int `json:"unmatched"`
	// Applied is how many entries changed locally because MAL had moved on.
	Applied int `json:"applied"`
}

// Pull applies site edits (entries that differ from what MAL was last told)
// locally, creates entries kuro lacks, and records the rest as MAL's state so
// connecting never replays the whole list back at it.
func (m *MALSync) Pull(ctx context.Context) (MALImport, error) {
	var rep MALImport
	if m.mal == nil || !m.mal.Authenticated() {
		return rep, nil
	}

	entries, err := m.mal.List(ctx)
	if err != nil {
		return rep, err
	}
	rep.Entries = len(entries)

	malIDs := make([]int, 0, len(entries))
	for _, e := range entries {
		malIDs = append(malIDs, e.AnimeID)
	}
	mapping, err := m.store.AniListIDsForMAL(ctx, malIDs)
	if err != nil {
		return rep, err
	}

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

		apply := false
		switch {
		case !seen:
			local, err := m.store.ListEntry(ctx, animeID)
			if err != nil {
				return rep, err
			}
			apply = local.AnimeID == 0
		default:
			apply = last.Progress != remote.Progress || last.Status != remote.Status || last.Score != remote.Score
		}

		if apply {
			changed, err := m.store.ApplyRemote(ctx, store.RemoteEntry{
				AnimeID: animeID, Status: remote.Status, Progress: remote.Progress,
				Score: remote.Score, Repeat: remote.Repeat,
			})
			if err != nil {
				return rep, err
			}
			if !changed {
				// An unpushed local edit wins; MAL learns of it next run.
				continue
			}
			rep.Applied++
		}
		if err := m.store.MarkPushed(ctx, malTracker, animeID, remote.Progress, remote.Status, remote.Score); err != nil {
			return rep, err
		}
	}

	if rep.Applied > 0 {
		m.log.Info("myanimelist changes applied", "entries", rep.Applied)
	}
	return rep, nil
}
