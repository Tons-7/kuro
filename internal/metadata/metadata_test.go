package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(t *testing.T, name string, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := New()
	c.SetURL(name, srv.URL)
	return c
}

// ani.zip emits both airDate and airdate for the same value, and keys specials
// "S1".."Sn" alongside numbered episodes.
const anizipJSON = `{
  "titles": {"en": "Frieren", "x-jat": "Sousou no Frieren"},
  "mappings": {"mal_id": 52991, "anidb_id": 17617, "thetvdb_id": 424536},
  "episodes": {
    "1": {"episode":"1","episodeNumber":1,"seasonNumber":1,"absoluteEpisodeNumber":1,
          "title":{"en":"The Journey's End","ja":"冒険の終わり"},
          "overview":"An overview.","image":"https://example/1.jpg",
          "airDate":"2023-09-29","airdate":"2023-09-29","runtime":24,"anidbEid":260525},
    "2": {"episode":"2","episodeNumber":2,"absoluteEpisodeNumber":2,
          "title":{"en":"It Didn't Have to Be Magic"},"airDate":"2023-09-29","runtime":24},
    "S1": {"episode":"S1","title":{"en":"A Special"},"airDate":"2024-01-01"}
  }
}`

func TestEpisodesSkipsSpecials(t *testing.T) {
	c := testClient(t, "anizip", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, anizipJSON)
	})

	got, err := c.Episodes(context.Background(), 154587)
	if err != nil {
		t.Fatal(err)
	}

	// Specials do not belong in a season's episode list.
	if len(got.Episodes) != 2 {
		t.Fatalf("got %d episodes, want 2", len(got.Episodes))
	}
	if got.MalID != 52991 || got.AniDBID != 17617 {
		t.Errorf("mappings = %+v", got)
	}

	var first Episode
	for _, e := range got.Episodes {
		if e.Number == 1 {
			first = e
		}
	}
	if first.TitleEN != "The Journey's End" {
		t.Errorf("title = %q", first.TitleEN)
	}
	if first.TitleJA != "冒険の終わり" {
		t.Errorf("japanese title = %q", first.TitleJA)
	}
	if first.AirDate == 0 {
		t.Error("air date not parsed")
	}
	if first.Runtime != 24 || first.AniDBID != 260525 {
		t.Errorf("episode = %+v", first)
	}
}

func TestEpisodesEmptyResponse(t *testing.T) {
	c := testClient(t, "anizip", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	// A show with no entry is a normal outcome, not a failure.
	got, err := c.Episodes(context.Background(), 1)
	if err != nil {
		t.Fatalf("a 404 should not be an error: %v", err)
	}
	if len(got.Episodes) != 0 {
		t.Fatalf("got %d episodes", len(got.Episodes))
	}
}

// The dataset is an array of shows, each with nested mappings and an array of
// episode objects.
const fillerJSON = `[
  {"slug":"naruto","title":"Naruto","mappings":{"anilist_id":20,"mal_id":20},
   "episodes":[
     {"episode":1,"title":"Enter","type":"manga-canon","aired_date":"2002-10-03"},
     {"episode":26,"title":"Special","type":"filler","aired_date":"2003-04-02"},
     {"episode":27,"title":"Partly","type":"mixed-manga","aired_date":"2003-04-09"},
     {"episode":28,"title":"Original","type":"anime-canon","aired_date":"2003-04-16"}
   ]},
  {"slug":"no-mappings","title":"Unmapped","mappings":{},
   "episodes":[{"episode":1,"type":"filler"}]}
]`

func TestFillersParsesAllCategories(t *testing.T) {
	c := testClient(t, "filler", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, fillerJSON)
	})

	got, err := c.Fillers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The unmapped show cannot be joined to anything.
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4", len(got))
	}

	kinds := map[int]string{}
	for _, f := range got {
		kinds[f.Episode] = f.Kind
		if f.MalID != 20 || f.AniListID != 20 {
			t.Errorf("episode %d mapping = mal %d anilist %d", f.Episode, f.MalID, f.AniListID)
		}
	}

	want := map[int]string{1: "manga-canon", 26: "filler", 27: "mixed", 28: "anime-canon"}
	for ep, kind := range want {
		if kinds[ep] != kind {
			t.Errorf("episode %d = %q, want %q", ep, kinds[ep], kind)
		}
	}
}

// A missing bulk dataset is a broken URL, not an empty result. Treating a 404
// as success reports a successful load of nothing.
func TestFillersTreatsMissingFileAsError(t *testing.T) {
	c := testClient(t, "filler", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if _, err := c.Fillers(context.Background()); err == nil {
		t.Fatal("a 404 on the bulk dataset must be an error")
	}
}

func TestFillersRejectsEmptyDataset(t *testing.T) {
	c := testClient(t, "filler", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[]`)
	})

	if _, err := c.Fillers(context.Background()); err == nil {
		t.Fatal("an empty dataset should be reported rather than silently accepted")
	}
}

func TestNormaliseKind(t *testing.T) {
	tests := map[string]string{
		"manga-canon": "manga-canon", "Manga Canon": "manga-canon",
		"anime-canon": "anime-canon", "mixed-manga": "mixed",
		"filler": "filler", "": "unknown",
	}
	for in, want := range tests {
		if got := normaliseKind(in); got != want {
			t.Errorf("normaliseKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSkips(t *testing.T) {
	c := testClient(t, "aniskip", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"found":true,"results":[
		  {"interval":{"startTime":128.0,"endTime":218.0},"skipType":"op","skipId":"a"},
		  {"interval":{"startTime":1343.0,"endTime":1433.0},"skipType":"ed","skipId":"b"},
		  {"interval":{"startTime":50.0,"endTime":50.0},"skipType":"recap","skipId":"c"}
		]}`)
	})

	got, err := c.Skips(context.Background(), 16498, 1, 1440)
	if err != nil {
		t.Fatal(err)
	}
	// A zero-length interval is unusable.
	if len(got) != 2 {
		t.Fatalf("got %d skips, want 2", len(got))
	}
	if got[0].Kind != "op" || got[0].Start != 128 || got[0].End != 218 {
		t.Errorf("opening = %+v", got[0])
	}
}

func TestSkipsNotFound(t *testing.T) {
	c := testClient(t, "aniskip", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"found":false,"results":[]}`)
	})

	got, err := c.Skips(context.Background(), 1, 1, 1440)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d skips, err %v", len(got), err)
	}

	// Without a MAL id there is nothing to query.
	if got, _ := c.Skips(context.Background(), 0, 1, 1440); got != nil {
		t.Error("querying without a mal id should do nothing")
	}
}

// Recap exists in no filler dataset, so this is the only source for it.
func TestFlagsReadsRecap(t *testing.T) {
	c := testClient(t, "tenrai", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[
		  {"mal_id":25,"filler":false,"recap":false},
		  {"mal_id":26,"filler":true,"recap":true},
		  {"mal_id":27,"filler":true,"recap":false}
		],"pagination":{"last_visible_page":1,"has_next_page":false}}`)
	})

	got, err := c.Flags(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	// Only episodes carrying a flag are worth storing.
	if len(got) != 2 {
		t.Fatalf("got %d flagged episodes, want 2", len(got))
	}

	var recap EpisodeFlags
	for _, f := range got {
		if f.Episode == 26 {
			recap = f
		}
	}
	// An episode can be filler and a recap at once.
	if !recap.Filler || !recap.Recap {
		t.Fatalf("episode 26 = %+v", recap)
	}
}

func TestFlagsWithoutMalID(t *testing.T) {
	c := New()
	if got, err := c.Flags(context.Background(), 0); got != nil || err != nil {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestParseDate(t *testing.T) {
	tests := map[string]bool{
		"2023-09-29":           true,
		"2023-09-29T00:00:00Z": true,
		"":                     false,
		"not a date":           false,
	}
	for in, wantParsed := range tests {
		got := parseDate(in)
		if (got != 0) != wantParsed {
			t.Errorf("parseDate(%q) = %d", in, got)
		}
	}
}

// Jikan has spent days answering 504 on every endpoint, so the flags fetch
// leads with Tenrai and keeps Jikan only as a second chance.
func TestFlagsFallsBackWhenThePrimaryIsDown(t *testing.T) {
	jikan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"mal_id":7,"filler":true,"recap":false}],
		  "pagination":{"last_visible_page":1,"has_next_page":false}}`)
	}))
	t.Cleanup(jikan.Close)

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
	}))
	t.Cleanup(dead.Close)

	c := New()
	c.SetURL("tenrai", dead.URL)
	c.SetURL("jikan", jikan.URL)

	got, err := c.Flags(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Episode != 7 {
		t.Fatalf("got %+v, want the fallback's episode 7", got)
	}
}

// The site's rows carry their classification in the row class and the episode
// number in its id, so neither depends on the cell layout holding still.
func TestFillerSiteEpisodesParsesRows(t *testing.T) {
	c := testClient(t, "fillershow", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `
		<table><tbody>
		<tr class="manga_canon odd" id="eps-1"><td class="Number">1</td></tr>
		<tr class="filler even" id="eps-2"><td class="Number">2</td></tr>
		<tr class="mixed_canon_filler odd" id="eps-3"><td class="Number">3</td></tr>
		<tr class="anime_canon even" id="eps-1187"><td class="Number">1187</td></tr>
		</tbody></table>`)
	})

	got, err := c.FillerSiteEpisodes(context.Background(), "detective-conan")
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{1: "manga-canon", 2: "filler", 3: "mixed", 1187: "anime-canon"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for _, f := range got {
		if want[f.Episode] != f.Kind {
			t.Errorf("episode %d = %q, want %q", f.Episode, f.Kind, want[f.Episode])
		}
	}
}

// Titles differ in punctuation and often carry the romanisation in brackets,
// so both halves have to resolve to the same show.
func TestFillerSiteShowsMatchesEitherTitle(t *testing.T) {
	c := testClient(t, "fillersite", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `
		<a href="/shows/detective-conan" rel="nofollow">Detective Conan</a>
		<a href="/shows/certain-magical-index">A Certain Magical Index (Toaru Majutsu No Index)</a>`)
	})

	index, err := c.FillerSiteShows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for title, want := range map[string]string{
		"Detective Conan":         "detective-conan",
		"detective conan":         "detective-conan",
		"A Certain Magical Index": "certain-magical-index",
		"Toaru Majutsu no Index":  "certain-magical-index",
	} {
		if got := index[NormaliseTitle(title)]; got != want {
			t.Errorf("%q resolved to %q, want %q", title, got, want)
		}
	}
}
