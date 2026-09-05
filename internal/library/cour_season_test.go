package library

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"kuro/internal/indexer"
	"kuro/internal/parse"
	"kuro/internal/score"
)

// A later cour restarts at one, but a release named for the broadcast season
// carries that season's number: The Calamity episode 2 ships as S17E42.
var broadcastForm = []struct {
	title    string
	accepted bool
}{
	{"[AnoZu] Bleach S17E42 1080p CR WEB-DL AAC 2.0 H.264", true},
	{"Bleach S17E42 Thousand-Year Blood War SON OF DARKNESS 1080p DSNP WEB-DL AAC2.0 H 264-NTb", true},
	{"[Breeze] Bleach Thousand-Year Blood War S17E42 [1080p AV1 Dual Audio] (weekly)", true},
	// The bare form of the same episode, which already works.
	{"[DKB] Bleach - Sennen Kessen-hen - 42 [1080p][HEVC x265 10bit][Multi-Subs][234F1874].mkv", true},
	// A different episode of the same broadcast season is not this one.
	{"[AnoZu] Bleach S17E43 1080p CR WEB-DL AAC 2.0 H.264", false},
}

func TestBroadcastSeasonReleasesReachALaterCour(t *testing.T) {
	st := bleachStore(t)
	ctx := context.Background()

	var results []indexer.Torrent
	for i, r := range broadcastForm {
		results = append(results, release(strings.Repeat("b", 39)+string(rune('0'+i)), r.title, 100))
	}
	f := NewFinder(st, fixedIndexer{results: results}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	found, err := f.Find(ctx, Request{
		AnimeID: tybwCalamity, Episode: 2, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}

	titles, _ := st.SearchTitles(ctx, tybwCalamity)
	english, _ := st.EnglishTitle(ctx, tybwCalamity)
	req := f.numbering(ctx, Request{AnimeID: tybwCalamity, Episode: 2}, titles, english)
	t.Logf("request: season=%d part=%d alias=%+v cour=%+v", req.Season, req.Part, req.Alias, req.Cour)
	for _, r := range broadcastForm {
		rel := parse.Parse(r.title)
		t.Logf("season=%2d ep=%3d verifies=%-5v confirms=%-5v numbers=%v  %s",
			rel.Season, rel.Episode, verifies(rel, req), confirms(rel, req), numbersFor(rel, req), r.title)
	}

	kept := map[string]bool{}
	for _, r := range found.Results {
		kept[r.Torrent.Title] = true
	}
	for _, want := range broadcastForm {
		if kept[want.title] != want.accepted {
			t.Errorf("accepted=%v, want %v: %s", kept[want.title], want.accepted, want.title)
		}
	}
	if found.Best == nil {
		t.Error("nothing pickable for episode 2 of the current cour")
	}
}
