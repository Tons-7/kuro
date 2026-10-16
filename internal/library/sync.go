package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

// Sync keeps the local database and AniList in agreement. Local is authoritative:
// push before pull, so our writes come back reconciled and unpushed edits survive.
type Sync struct {
	store    *store.Store
	al       *anilist.Client
	mal      *MALSync
	importer *Importer
	log      *slog.Logger

	// One push at a time, or the job's older value can land last.
	pushing sync.Mutex
}

func NewSync(s *store.Store, al *anilist.Client, log *slog.Logger) *Sync {
	return &Sync{store: s, al: al, log: log}
}

// WithMAL mirrors each episode to MyAnimeList as it is watched, rather than leaving it to the periodic job.
func (s *Sync) WithMAL(m *MALSync) *Sync {
	s.mal = m
	return s
}

// WithImporter enables the pull half: without it Run only pushes.
func (s *Sync) WithImporter(i *Importer) *Sync {
	s.importer = i
	return s
}

type SyncReport struct {
	Pushed int `json:"pushed"`
	Failed int `json:"failed"`
	// Pulled is the size of the list as AniList holds it after the merge.
	Pulled int `json:"pulled"`
	// Favourites is how many the tracker holds after the merge.
	Favourites int `json:"favourites"`
}

// AuthErrorSetting is set when AniList refuses the token, cleared by a sync or a new login.
const AuthErrorSetting = "anilist.auth_error"

var ErrReconnect = errors.New("anilist rejected the token; reconnect AniList in Settings")

// pushBatch is how many dirty entries one run sends: a 300-entry import drains in a few runs, not a dozen.
const pushBatch = 100

func (s *Sync) Run(ctx context.Context) (SyncReport, error) {
	rep, err := s.run(ctx)
	if err == nil && s.al.Authenticated() {
		if v, _ := s.store.Setting(ctx, AuthErrorSetting); v != "" {
			s.store.SetSetting(ctx, AuthErrorSetting, "")
		}
	}
	return rep, err
}

func (s *Sync) run(ctx context.Context) (SyncReport, error) {
	var rep SyncReport
	if !s.al.Authenticated() {
		return rep, nil
	}

	// Rows AniList refused come last, so they cannot take every slot of every run.
	dirty, err := s.store.DirtyEntries(ctx, pushBatch)
	if err != nil {
		return rep, err
	}

	for _, d := range dirty {
		if err := s.pushLatest(ctx, d.AnimeID, false); err != nil {
			rep.Failed++
			s.log.Warn("push progress", "anime", d.AnimeID, "err", err)

			// Retrying would only burn the rate limit; recorded so the tracker reads "reconnect".
			if errs, ok := err.(anilist.Errors); ok && errs.Unauthorized() {
				s.store.SetSetting(ctx, AuthErrorSetting, "true")
				return rep, ErrReconnect
			}
			if err := s.store.PushFailed(ctx, "anilist", d.AnimeID); err != nil {
				return rep, err
			}
			continue
		}
		rep.Pushed++
	}

	if rep.Pushed > 0 {
		s.log.Info("progress synced", "entries", rep.Pushed)
	}

	// After the push: the merge skips dirty rows, so a failed push is not reverted.
	userID, _ := s.store.SettingInt(ctx, "anilist.user_id")
	if s.importer != nil && userID > 0 {
		res, err := s.importer.Run(ctx, userID, store.ImportMerge)
		if errs, ok := errors.AsType[anilist.Errors](err); ok && errs.Unauthorized() {
			s.store.SetSetting(ctx, AuthErrorSetting, "true")
			return rep, ErrReconnect
		}
		if err != nil {
			return rep, err
		}
		rep.Pulled = res.Entries
	}

	if userID > 0 {
		held, err := s.favourites(ctx, userID)
		if err != nil {
			s.log.Warn("sync favourites", "err", err)
		} else {
			rep.Favourites = held
		}
	}
	return rep, nil
}

// favourites reconciles both directions. AniList only toggles, so the remote
// list is read first and a change is sent only where the two disagree.
func (s *Sync) favourites(ctx context.Context, userID int) (int, error) {
	remote, err := s.al.Favourites(ctx, userID)
	if err != nil {
		return 0, err
	}

	dirty, err := s.store.DirtyFavourites(ctx)
	if err != nil {
		return 0, err
	}

	on := make(map[int]bool, len(remote))
	for _, id := range remote {
		on[id] = true
	}

	for _, change := range dirty {
		if on[change.AnimeID] != change.Favourite {
			if err := s.al.ToggleFavourite(ctx, change.AnimeID); err != nil {
				s.log.Warn("push favourite", "anime", change.AnimeID, "err", err)
				continue
			}
			on[change.AnimeID] = change.Favourite
		}
		if err := s.store.MarkFavouriteSynced(ctx, change.AnimeID, change.Favourite); err != nil {
			return 0, err
		}
	}

	ids := make([]int, 0, len(on))
	for id, fav := range on {
		if fav {
			ids = append(ids, id)
		}
	}
	held, err := s.store.ApplyRemoteFavourites(ctx, ids)
	if err != nil {
		return 0, err
	}

	// A favourite the corpus never saw is an "Unknown" placeholder until fetched.
	if s.importer != nil {
		missing, err := s.store.Unhydrated(ctx, ids)
		if err != nil {
			return held, err
		}
		if _, err := s.importer.Hydrate(ctx, missing); err != nil {
			s.log.Warn("hydrate favourites", "err", err)
		}
	}
	return held, nil
}

// PushOne sends one anime to every connected tracker now.
func (s *Sync) PushOne(ctx context.Context, animeID int) error {
	var errs []error
	if s.mal != nil {
		if err := s.mal.PushOne(ctx, animeID); err != nil {
			errs = append(errs, err)
		}
	}
	if s.al.Authenticated() && animeID > 0 {
		// Forced: a run's clear in the same second can mark this change clean.
		if err := s.pushLatest(ctx, animeID, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// pushLatest sends the entry as it is now, unless clean and not forced.
func (s *Sync) pushLatest(ctx context.Context, animeID int, force bool) error {
	s.pushing.Lock()
	defer s.pushing.Unlock()
	entry, err := s.store.ListEntry(ctx, animeID)
	if err != nil || entry.AnimeID == 0 || (!entry.Dirty && !force) {
		return err
	}
	return s.push(ctx, entry)
}

func (s *Sync) push(ctx context.Context, d store.DirtyEntry) error {
	var completed *anilist.FuzzyDate
	if strings.EqualFold(d.Status, "COMPLETED") {
		completed = fuzzyDate(d.CompletedAt)
	}

	saved, err := s.al.SetProgress(ctx, d.AnimeID, d.Progress, d.Status, completed, d.Repeat, d.Score,
		d.ScoreCleared && d.Score == 0)
	if err != nil {
		return err
	}
	return s.store.ClearDirtyAt(ctx, d.AnimeID, saved.ID, saved.UpdatedAt, d.Changed)
}

// AniList wants year, month and day as separate integers.
func fuzzyDate(s string) *anilist.FuzzyDate {
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return nil
	}
	year, err1 := strconv.Atoi(parts[0])
	month, err2 := strconv.Atoi(parts[1])
	day, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || year == 0 {
		return nil
	}
	return &anilist.FuzzyDate{Year: &year, Month: &month, Day: &day}
}

// Watched is called when playback passes the threshold. It records progress
// locally and pushes immediately, so a quick app close still updates the list.
func (s *Sync) Watched(ctx context.Context, animeID, episode int) error {
	changed, err := s.store.MarkWatched(ctx, animeID, episode)
	if err != nil || !changed {
		return err
	}
	s.log.Info("episode watched", "anime", animeID, "episode", episode)

	return s.PushOne(ctx, animeID)
}

// Remove takes a show off the list on AniList, then MAL, then here; a tracker failing leaves it all on.
// The push lock is held throughout, so a push already in flight can't recreate the entry after.
func (s *Sync) Remove(ctx context.Context, animeID int) error {
	s.pushing.Lock()
	defer s.pushing.Unlock()
	entry, err := s.store.ListEntry(ctx, animeID)
	if err != nil {
		return err
	}
	// A negative id is a row only kuro has; AniList never saw it.
	if entry.ID > 0 && s.al.Authenticated() {
		if err := s.al.DeleteEntry(ctx, entry.ID); err != nil {
			return fmt.Errorf("remove from AniList: %w", err)
		}
	}
	if s.mal != nil {
		if err := s.mal.Remove(ctx, animeID); err != nil {
			return fmt.Errorf("remove from MyAnimeList: %w", err)
		}
	}
	return s.store.RemoveListEntry(ctx, animeID)
}
