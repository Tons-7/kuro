package library

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

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
}

func NewSync(s *store.Store, al *anilist.Client, log *slog.Logger) *Sync {
	return &Sync{store: s, al: al, log: log}
}

// WithMAL mirrors each episode to MyAnimeList as it is watched, rather than
// leaving it to the periodic job.
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

func (s *Sync) Run(ctx context.Context) (SyncReport, error) {
	var rep SyncReport
	if !s.al.Authenticated() {
		return rep, nil
	}

	dirty, err := s.store.DirtyEntries(ctx, 0)
	if err != nil {
		return rep, err
	}

	for _, d := range dirty {
		if err := s.push(ctx, d); err != nil {
			rep.Failed++
			s.log.Warn("push progress", "anime", d.AnimeID, "err", err)

			// Re-authentication is the user's job; retrying every entry would
			// just burn the rate limit.
			if errs, ok := err.(anilist.Errors); ok && errs.Unauthorized() {
				return rep, nil
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

	// A favourite the corpus has never seen arrives as a placeholder row
	// titled "Unknown" until something fetches it.
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
		entry, err := s.store.ListEntry(ctx, animeID)
		if err != nil {
			errs = append(errs, err)
		} else if entry.AnimeID != 0 {
			if err := s.push(ctx, entry); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Sync) push(ctx context.Context, d store.DirtyEntry) error {
	var completed *anilist.FuzzyDate
	if strings.EqualFold(d.Status, "COMPLETED") {
		completed = fuzzyDate(d.CompletedAt)
	}

	saved, err := s.al.SetProgress(ctx, d.AnimeID, d.Progress, d.Status, completed, d.Repeat, d.Score)
	if err != nil {
		return err
	}
	return s.store.ClearDirty(ctx, d.AnimeID, saved.ID, saved.UpdatedAt)
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
