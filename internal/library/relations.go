package library

import (
	"context"
	"log/slog"
	"slices"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

// Relations builds the franchise graph. AniList models each season as its own
// entry linked by prequel/sequel edges, which stitch separate rows into one show.
type Relations struct {
	store *store.Store
	al    *anilist.Client
	log   *slog.Logger
}

func NewRelations(s *store.Store, al *anilist.Client, log *slog.Logger) *Relations {
	return &Relations{store: s, al: al, log: log}
}

// Only prequel/sequel edges are seasons; spin-offs would wedge a recap mid-show.
var seasonEdges = map[string]bool{"PREQUEL": true, "SEQUEL": true}

// Fetch walks outward from one anime, following season edges until the whole
// franchise is covered. Depth is bounded since a franchise can chain many entries.
func (r *Relations) Fetch(ctx context.Context, animeID int) (int, error) {
	const maxHops = 6

	seen := map[int]bool{animeID: true}
	frontier := []int{animeID}
	var saved []store.Relation

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		media, err := r.al.Relations(ctx, frontier)
		if err != nil {
			return 0, err
		}

		var next []int
		for _, m := range media {
			for _, edge := range m.Relations.Edges {
				if !seasonEdges[edge.Type] || edge.Node.Type != "ANIME" {
					continue
				}
				saved = append(saved, store.Relation{
					AnimeID: m.ID, RelatedID: edge.Node.ID, Kind: edge.Type,
				})
				if !seen[edge.Node.ID] {
					seen[edge.Node.ID] = true
					next = append(next, edge.Node.ID)
				}
			}
		}
		frontier = next
	}

	// Recorded, or Ensure asks AniList again on every visit to a standalone show.
	if len(saved) == 0 {
		return 0, r.store.MarkNoRelations(ctx, animeID)
	}
	if err := r.store.SaveRelations(ctx, saved); err != nil {
		return 0, err
	}
	r.nameMembers(ctx, seen)

	groups, err := r.store.RebuildFranchises(ctx)
	if err != nil {
		return 0, err
	}
	r.log.Info("franchise resolved", "anime", animeID, "edges", len(saved), "groups", groups)
	return len(saved), nil
}

// nameMembers stores titles for franchise members that have none: the walk
// records ids, and a sibling cour is only told apart by name.
func (r *Relations) nameMembers(ctx context.Context, members map[int]bool) {
	const maxFetch = 25

	var missing []int
	for id := range members {
		names, err := r.store.SearchTitles(ctx, id)
		if err == nil && len(names) == 0 {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	slices.Sort(missing)
	if len(missing) > maxFetch {
		missing = missing[:maxFetch]
	}

	if _, err := NewImporter(r.store, r.al, r.log).Hydrate(ctx, missing); err != nil {
		r.log.Warn("franchise titles", "ids", len(missing), "err", err)
	}
}

// Ensure fetches relations the first time a show is opened. Names are checked
// every visit: a franchise walked before they mattered has none.
func (r *Relations) Ensure(ctx context.Context, animeID int) error {
	known, err := r.store.HasRelations(ctx, animeID)
	if err != nil {
		return err
	}
	if !known {
		_, err = r.Fetch(ctx, animeID)
		return err
	}

	franchise, err := r.store.Franchise(ctx, animeID)
	if err != nil {
		return err
	}
	members := make(map[int]bool, len(franchise.Seasons))
	for _, s := range franchise.Seasons {
		members[s.ID] = true
	}
	r.nameMembers(ctx, members)
	return nil
}
