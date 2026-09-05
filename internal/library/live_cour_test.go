package library

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/indexer"
	"kuro/internal/metadata"
	"kuro/internal/parse"
	"kuro/internal/score"
	"kuro/internal/store"
)

// Currently airing, chosen for the shapes that break matching: long runs with
// absolute numbering, later cours named by subtitle, numbered sequels, and
// ordinary first cours as a control.
var airing = []struct {
	id      int
	episode int
	name    string
}{
	{21, 1176, "One Piece"},
	{235, 1211, "Detective Conan"},
	{185874, 2, "Bleach TYBW: The Calamity"},
	{185874, 5, "Bleach TYBW: The Calamity"},
	{182205, 21, "Slime 4th Season"},
	{189046, 15, "Re:Zero 4th Season"},
	{178789, 10, "Mushoku Tensei III"},
	{135865, 9, "Youjo Senki II"},
	{195600, 21, "Yomi no Tsugai"},
	{210031, 9, "Seihantai na Kimi to Boku 2nd Season"},
	{187538, 9, "BLACK TORCH"},
	{196187, 9, "Super no Ura de Yani Suu Futari"},
}

// TestLiveAiringShowsResolveARelease seeds each show through the same importer,
// relation walk and enricher the app runs, then asks the real indexers. A pick
// naming another cour counts as a miss, not a hit.
// Opt-in: KURO_LIVE=1 go test ./internal/library -run LiveAiring -v
func TestLiveAiringShowsResolveARelease(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to search the live indexers")
	}

	st := prefetchStore(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	al := anilist.New(log)
	f := NewFinder(st, liveSources(t), log)

	seeded := map[int]bool{}
	for _, show := range airing {
		if !seeded[show.id] {
			seedLikeTheApp(t, st, al, log, show.id)
			seeded[show.id] = true
		}
		t.Run(fmt.Sprintf("%s ep%d", show.name, show.episode), func(t *testing.T) {
			runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()

			titles, _ := st.SearchTitles(runCtx, show.id)
			english, _ := st.EnglishTitle(runCtx, show.id)
			req := f.numbering(runCtx, Request{AnimeID: show.id, Episode: show.episode}, titles, english)

			got, err := f.Find(runCtx, Request{
				AnimeID: show.id, Episode: show.episode, Prefs: score.DefaultPreferences(),
			})
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			t.Logf("alias=%+v later=%v own=%v siblings=%v candidates=%d",
				req.Alias, req.Cour.Later, req.Cour.Own, req.Cour.Siblings, len(got.Results))
			if got.Best == nil {
				t.Errorf("no release resolved")
				return
			}
			t.Logf("picked %q numbers=%v confirmed=%v (%d seeders)",
				got.Best.Torrent.Title, got.Best.Numbers, got.Best.Confirmed, got.Best.Torrent.Seeders)
			if word := sibling(got.Best.Torrent.Title, req); word != "" {
				t.Errorf("picked another cour (%q): %s", word, got.Best.Torrent.Title)
			}
			if n := parse.Parse(got.Best.Torrent.Title).Episode; n > 0 && !slices.Contains(got.Best.Numbers, n) {
				t.Errorf("picked episode %d, wanted %v: %s", n, got.Best.Numbers, got.Best.Torrent.Title)
			}
		})
	}
}

// liveSources reads the sites to search from KURO_NYAA and KURO_TOKYOTOSHO, so
// no site is named in the repository.
func liveSources(t *testing.T) indexer.Source {
	t.Helper()
	var sites []indexer.Source
	if u := os.Getenv("KURO_NYAA"); u != "" {
		sites = append(sites, indexer.NewCached(indexer.NewNyaa(u)))
	}
	if u := os.Getenv("KURO_TOKYOTOSHO"); u != "" {
		sites = append(sites, indexer.NewCached(indexer.NewTokyoTosho(u)))
	}
	if len(sites) == 0 {
		t.Skip("set KURO_NYAA and/or KURO_TOKYOTOSHO to the sites to search")
	}
	return indexer.Multi{Sources: sites}
}

// sibling names the cour word that makes a title another entry's, if any.
func sibling(title string, req Request) string {
	words := strings.Fields(stripSymbols(strings.ToLower(title)))
	for _, w := range req.Cour.Siblings {
		if slices.Contains(words, w) && !slices.Contains(req.Cour.Own, w) {
			return w
		}
	}
	return ""
}

// seedLikeTheApp stores what a first visit to a show's page stores: the AniList
// media, the franchise edges around it, and the episode list from ani.zip.
func seedLikeTheApp(t *testing.T, st *store.Store, al *anilist.Client, log *slog.Logger, animeID int) {
	t.Helper()
	ctx := context.Background()

	media, err := al.MediaByIDs(ctx, []int{animeID})
	if err != nil || len(media) == 0 {
		t.Fatalf("anilist %d: %v", animeID, err)
	}
	if _, err := NewImporter(st, al, log).Save(ctx, media); err != nil {
		t.Fatal(err)
	}
	if err := NewRelations(st, al, log).Ensure(ctx, animeID); err != nil {
		t.Fatal(err)
	}
	n, err := NewEnricher(st, metadata.New(), log).Episodes(ctx, animeID)
	if err != nil {
		t.Fatal(err)
	}
	alias, _ := st.EpisodeAlias(ctx, animeID, 1)
	t.Logf("seeded %d: %d episodes, ep1 alias=%+v", animeID, n, alias)
}
