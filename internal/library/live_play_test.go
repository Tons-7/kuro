package library

import (
	"context"
	"os"
	"testing"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/score"
)

// Hitting play on the kinds that break differently: long-runner, split cour,
// film. Opt-in: KURO_LIVE=1 go test ./internal/library -run LivePlays -v
func TestLivePlaysAcrossShows(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to search the live indexers")
	}

	// numbers are the ones a release may carry for that episode: a later cour
	// is also named by its absolute number.
	shows := []struct {
		id      int
		episode int
		numbers []int
		name    string
		titles  []string
	}{
		{185874, 5, []int{5, 45}, "Bleach TYBW cour 4", []string{
			"BLEACH: Sennen Kessen-hen - Kashin-tan", "BLEACH: Thousand-Year Blood War - The Calamity"}},
		{21, 1100, []int{1100}, "One Piece", []string{"ONE PIECE", "One Piece"}},
		{235, 1206, []int{1206}, "Detective Conan", []string{"Meitantei Conan", "Detective Conan", "Case Closed"}},
		{127230, 8, []int{8}, "Chainsaw Man", []string{"Chainsaw Man"}},
		{1535, 12, []int{12}, "Death Note", []string{"DEATH NOTE", "Death Note"}},
		{21519, 1, []int{1}, "Kimi no Na wa. (film)", []string{"Kimi no Na wa.", "Your Name."}},
		{3783, 1, []int{1}, "Kara no Kyoukai 3 (58-minute film)", []string{
			"Kara no Kyoukai: Tsuukaku Zanryuu", "the Garden of sinners Chapter 3"}},
	}

	st := prefetchStore(t)
	ctx := context.Background()
	al := anilist.New(discard())

	ids := make([]int, 0, len(shows))
	for _, s := range shows {
		ids = append(ids, s.id)
	}
	if _, err := NewImporter(st, al, discard()).Hydrate(ctx, ids); err != nil {
		t.Fatal(err)
	}

	relations := NewRelations(st, al, discard())
	finder := NewFinder(st, liveSources(t), discard())

	for _, s := range shows {
		t.Run(s.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			if err := relations.Ensure(ctx, s.id); err != nil {
				t.Logf("relations: %v", err)
			}

			got, err := finder.Find(ctx, Request{
				AnimeID: s.id, Episode: s.episode, Season: 1,
				Prefs: score.DefaultPreferences(),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("queries: %v — %d results", got.Queries, len(got.Results))
			for i, r := range got.Results {
				if i == 8 {
					break
				}
				blocked := ""
				if r.Blocked != "" {
					blocked = " — REJECTED: " + r.Blocked
				}
				t.Logf("  %6.0f %s%s", r.Score, r.Torrent.Title, blocked)
			}
			if got.Best == nil {
				t.Fatalf("no release for episode %d among %d results", s.episode, len(got.Results))
			}
			best := got.Best
			t.Logf("PICKED %s (%.2f GiB/ep, %d seeders, score %.0f)",
				best.Torrent.Title, float64(best.EpisodeBytes())/(1<<30),
				best.Torrent.Seeders, best.Score)

			if !identifiesShow(best.Release, best.Torrent.Title, s.titles) {
				t.Errorf("picked %q, which does not name %s", best.Torrent.Title, s.name)
			}
			// The right show at the wrong episode is the same dead end as none.
			// A film is the whole entry, so it numbers nothing.
			rel := best.Release
			right := s.episode == 1 && rel.Episode == 0 && !rel.Batch
			for _, n := range s.numbers {
				right = right || rel.Episode == n || covers(rel, n)
			}
			if !right {
				t.Errorf("picked %q for episode %d: it carries episode %d-%d",
					best.Torrent.Title, s.episode, rel.Episode, rel.EpisodeEnd)
			}
			if best.Torrent.SeedersKnown && best.Torrent.Seeders == 0 {
				t.Errorf("picked %q with a dead swarm", best.Torrent.Title)
			}
		})
	}
}
