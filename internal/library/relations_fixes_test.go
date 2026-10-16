package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

// relationsServer answers the Relations query from a table of edges per id; other queries get nothing.
func relationsServer(t *testing.T, answers map[int]string, asked *atomic.Int32) *anilist.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Relations") {
			io.WriteString(w, `{"data":{"Page":{"media":[]}}}`)
			return
		}
		asked.Add(1)
		var media []string
		for id, m := range answers {
			if strings.Contains(string(body), `"ids":[`) && !idIn(string(body), id) {
				continue
			}
			media = append(media, m)
		}
		io.WriteString(w, `{"data":{"Page":{"media":[`+strings.Join(media, ",")+`]}}}`)
	}))
	t.Cleanup(srv.Close)
	return anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
}

func idIn(body string, id int) bool {
	start := strings.Index(body, `"ids":[`)
	end := strings.Index(body[start:], "]")
	for part := range strings.SplitSeq(body[start+7:start+end], ",") {
		if strings.TrimSpace(part) == itoa(id) {
			return true
		}
	}
	return false
}

func itoa(n int) string { return strconv.Itoa(n) }

func tvEntry(id int, start string) store.Anime {
	tv := "TV"
	eps := 12
	return store.Anime{ID: id, Romaji: "Show " + itoa(id), Format: &tv, Episodes: &eps, StartDate: start, Synonyms: "[]", Genres: "[]"}
}

func media(id int, edges ...string) string {
	return `{"id":` + itoa(id) + `,"episodes":12,"startDate":{"year":2020,"month":1,"day":` + itoa(id) + `},"relations":{"edges":[` + strings.Join(edges, ",") + `]}}`
}

func edge(kind string, id int, format string) string {
	return `{"relationType":"` + kind + `","node":{"id":` + itoa(id) + `,"type":"ANIME","format":"` + format + `","episodes":12,"startDate":{"year":2020,"month":1,"day":` + itoa(id) + `}}}`
}

// A series reached by a PARENT or SPIN_OFF edge is not stamped as walked: its own chain is asked for when
// it is opened. A film reached the same way is, since its chain is this one.
func TestWalkStampsOnlyWhatItAskedFor(t *testing.T) {
	var asked atomic.Int32
	al := relationsServer(t, map[int]string{
		1: media(1, edge("PARENT", 2, "TV"), edge("SIDE_STORY", 3, "MOVIE")),
		2: media(2, edge("SIDE_STORY", 1, "OVA"), edge("SEQUEL", 4, "TV")),
		4: media(4, edge("PREQUEL", 2, "TV")),
	}, &asked)
	st := prefetchStore(t)
	ctx := context.Background()
	for _, a := range []store.Anime{tvEntry(1, "2021-01-01"), tvEntry(2, "2020-01-01"), tvEntry(4, "2022-01-01")} {
		if _, err := st.ImportList(ctx, []store.Anime{a}, nil, store.ImportMerge); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRelations(st, al, discard())
	if err := r.Ensure(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if st.RelationsStale(ctx, 3, relationsMaxAge) {
		t.Error("the film reached by the walk should count as walked")
	}
	if !st.RelationsStale(ctx, 2, relationsMaxAge) {
		t.Error("the parent series was stamped as walked without being asked for")
	}

	before := asked.Load()
	if err := r.Ensure(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if asked.Load() == before {
		t.Fatal("opening the parent series asked AniList nothing")
	}
	fr, _ := st.Franchise(ctx, 2)
	if len(fr.Seasons) != 2 {
		t.Fatalf("franchise of 2 = %+v, want its sequel chain", fr.Seasons)
	}
}

// AniList answering nothing for the show (hidden adult entry) is not a standalone show: asked again next time.
func TestNoAnswerIsNotAStandaloneShow(t *testing.T) {
	var asked atomic.Int32
	al := relationsServer(t, map[int]string{}, &asked)
	st := prefetchStore(t)
	ctx := context.Background()
	if _, err := st.ImportList(ctx, []store.Anime{tvEntry(1, "2021-01-01")}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	r := NewRelations(st, al, discard())
	if err := r.Ensure(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if known, _ := st.HasRelations(ctx, 1); known {
		t.Fatal("a show AniList did not answer for was recorded as standalone")
	}
	// A fresh client, past the response cache: the next visit asks again.
	r.al = relationsServer(t, map[int]string{}, &asked)
	if err := r.Ensure(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if known, _ := st.HasRelations(ctx, 1); known || asked.Load() != 2 {
		t.Fatalf("known=%v asked=%d, want each visit to ask again", known, asked.Load())
	}
}

// An edge AniList no longer reports is gone after the next walk, and a dead member is left out of the seasons.
func TestWalkReplacesTheEdgesOfWhatItFetched(t *testing.T) {
	var asked atomic.Int32
	answers := map[int]string{
		1: media(1, edge("SEQUEL", 2, "TV")),
		2: media(2, edge("PREQUEL", 1, "TV")),
	}
	al := relationsServer(t, answers, &asked)
	st := prefetchStore(t)
	ctx := context.Background()
	for _, a := range []store.Anime{tvEntry(1, "2020-01-01"), tvEntry(2, "2021-01-01")} {
		if _, err := st.ImportList(ctx, []store.Anime{a}, nil, store.ImportMerge); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRelations(st, al, discard())
	if err := r.Ensure(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if fr, _ := st.Franchise(ctx, 1); len(fr.Seasons) != 2 {
		t.Fatalf("seasons = %+v", fr.Seasons)
	}

	// AniList re-types the edge: 2 is a summary now, not a season.
	answers[1] = media(1, edge("SUMMARY", 2, "TV"))
	answers[2] = media(2, edge("PARENT", 1, "TV"))
	r.al = relationsServer(t, answers, &asked)
	if _, err := r.Fetch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if fr, _ := st.Franchise(ctx, 1); len(fr.Seasons) != 1 {
		t.Fatalf("seasons after the re-walk = %+v, want the old SEQUEL edge gone", fr.Seasons)
	}
}

// Members kuro never showed get the walk's dates, so an earlier season isn't sorted last.
func TestWalkDatesOrderMembersWithoutARow(t *testing.T) {
	var asked atomic.Int32
	al := relationsServer(t, map[int]string{
		5: media(5, edge("PREQUEL", 4, "TV")),
		4: media(4, edge("SEQUEL", 5, "TV")),
	}, &asked)
	st := prefetchStore(t)
	ctx := context.Background()
	// Only the later season has an anime row; the earlier one is known by name alone.
	if _, err := st.ImportList(ctx, []store.Anime{tvEntry(5, "2020-01-05")}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveCorpus(ctx, []store.CorpusEntry{{AniListID: 4, Kind: "TV", Episodes: 12,
		Titles: []store.CorpusTitle{{Text: "Show 4", Kind: "primary"}}}}); err != nil {
		t.Fatal(err)
	}
	r := NewRelations(st, al, discard())
	if err := r.Ensure(ctx, 5); err != nil {
		t.Fatal(err)
	}
	fr, _ := st.Franchise(ctx, 5)
	if len(fr.Seasons) != 2 || fr.Seasons[0].ID != 4 {
		t.Fatalf("seasons = %+v, want the earlier season first", fr.Seasons)
	}
}
