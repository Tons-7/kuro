package library

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"kuro/internal/metadata"
	"kuro/internal/store"
)

const listingJSON = `{"data":[
  {"mal_id":1,"title":"Ilse's Notebook","title_japanese":"イルゼの手帳","aired":"2013-12-09T00:00:00+00:00","duration":1398,"synopsis":"A scout writes."},
  {"mal_id":2,"title":"Episode 2","aired":null}
],"pagination":{"last_visible_page":1,"has_next_page":false}}`

// listed is a store holding one OVA of that many parts, and an enricher whose MyAnimeList answers with two episodes.
func listed(t *testing.T, status string, parts int) (*Enricher, *store.Store, *atomic.Int32) {
	t.Helper()
	st := openStore(t)
	a := entryOf(1, "OVA", "Some OVA", "", "2013-12-09", parts, 24)
	malID := 77
	a.MalID, a.Status = &malID, &status
	if _, err := st.ImportList(context.Background(), []store.Anime{a}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		io.WriteString(w, listingJSON)
	}))
	t.Cleanup(srv.Close)
	meta := metadata.New()
	meta.SetURL("tenrai", srv.URL)
	return NewEnricher(st, meta, slog.New(slog.DiscardHandler)), st, &asked
}

// A finished show the episode source has nothing for gets its episodes named from MyAnimeList.
func TestListingNamesAFinishedShowNothingDescribes(t *testing.T) {
	e, st, asked := listed(t, "FINISHED", 2)
	ctx := context.Background()
	e.listing(ctx, 1, 77)

	eps, err := st.Episodes(ctx, 1)
	if err != nil || len(eps) != 2 {
		t.Fatalf("episodes = %+v, err %v", eps, err)
	}
	first := eps[0]
	if first.TitleEN == nil || *first.TitleEN != "Ilse's Notebook" || first.Overview == nil || *first.Overview != "A scout writes." {
		t.Errorf("episode 1 = %+v", first)
	}
	if first.AirDate == nil || first.Runtime == nil || *first.Runtime != 23 {
		t.Errorf("episode 1 air date %v, runtime %v", first.AirDate, first.Runtime)
	}
	// "Episode 2" is MyAnimeList's placeholder, not a title.
	if eps[1].TitleEN != nil {
		t.Errorf("episode 2 title = %q", *eps[1].TitleEN)
	}

	// Asked once a month, not on every page load.
	e.listing(ctx, 1, 77)
	if asked.Load() != 1 {
		t.Errorf("asked %d times", asked.Load())
	}
}

// MyAnimeList's list lags an airing show, and a named row ends the padding to the episode count.
func TestListingLeavesAnAiringShowAlone(t *testing.T) {
	e, st, asked := listed(t, "RELEASING", 3)
	e.listing(context.Background(), 1, 77)

	if asked.Load() != 0 {
		t.Errorf("asked %d times about an airing show", asked.Load())
	}
	if eps, _ := st.Episodes(context.Background(), 1); len(eps) != 3 {
		t.Errorf("an airing show lists %d of its 3 episodes", len(eps))
	}
}

// A list naming two of three parts would hide the third, so it is not used.
func TestListingShorterThanTheShowIsNotUsed(t *testing.T) {
	e, st, _ := listed(t, "FINISHED", 3)
	ctx := context.Background()
	e.listing(ctx, 1, 77)

	eps, _ := st.Episodes(ctx, 1)
	if len(eps) != 3 || eps[0].TitleEN != nil {
		t.Fatalf("episodes = %+v, want all three still listed", eps)
	}
}

// A show the episode source did describe is not asked about.
func TestListingLeavesADescribedShowAlone(t *testing.T) {
	e, st, asked := listed(t, "FINISHED", 3)
	ctx := context.Background()
	if _, err := st.SaveEpisodes(ctx, 1, []metadata.Episode{{Number: 1, TitleEN: "From the source"}}); err != nil {
		t.Fatal(err)
	}
	e.listing(ctx, 1, 77)

	if asked.Load() != 0 {
		t.Errorf("asked %d times", asked.Load())
	}
}

// Only gaps are filled: what was recorded stays, and so do rows the list does not have.
func TestFillEpisodesKeepsWhatIsRecorded(t *testing.T) {
	_, st, _ := listed(t, "FINISHED", 3)
	ctx := context.Background()
	if _, err := st.SaveDerivedEpisodes(ctx, 1, []int{1, 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetEpisodeStills(ctx, 1, map[int]string{1: "still.jpg"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FillEpisodes(ctx, 1, []metadata.Episode{{Number: 1, TitleEN: "One"}, {Number: 2, TitleEN: "Two"}}); err != nil {
		t.Fatal(err)
	}

	eps, _ := st.Episodes(ctx, 1)
	if len(eps) != 3 {
		t.Fatalf("got %d episodes, want the derived third kept", len(eps))
	}
	if *eps[0].TitleEN != "One" || eps[0].Still == nil || *eps[0].Still != "still.jpg" || *eps[1].TitleEN != "Two" {
		t.Errorf("episodes = %+v", eps)
	}
}
