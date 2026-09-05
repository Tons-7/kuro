package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

// The walk records ids; matching needs names. A sibling with no title anywhere
// is invisible to the cour rules, and its episode 2 then passes for this one's.
func TestEnsureNamesTheFranchiseMembersItFinds(t *testing.T) {
	var queries int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		queries++
		if strings.Contains(string(body), "Relations") {
			io.WriteString(w, `{"data":{"Page":{"media":[{"id":1,"episodes":13,
			  "relations":{"edges":[{"relationType":"SEQUEL",
			    "node":{"id":2,"type":"ANIME","format":"TV","episodes":13}}]}}]}}}`)
			return
		}
		io.WriteString(w, `{"data":{"Page":{"media":[
		  {"id":2,"title":{"romaji":"Show - Second Cour"},"format":"TV","episodes":13,
		   "startDate":{"year":2025,"month":1,"day":1}}]}}}`)
	}))
	defer srv.Close()

	st := prefetchStore(t)
	ctx := context.Background()
	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	first := 13
	tv := "TV"
	if _, err := st.ImportList(ctx, []store.Anime{{
		ID: 1, Romaji: "Show", Format: &tv, Episodes: &first,
		StartDate: "2024-01-01", Synonyms: "[]", Genres: "[]",
	}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	r := NewRelations(st, al, discard())
	if err := r.Ensure(ctx, 1); err != nil {
		t.Fatal(err)
	}
	names, err := st.SearchTitles(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("the sibling the walk found was left unnamed")
	}

	// Named once: a second visit asks AniList nothing.
	before := queries
	if err := r.Ensure(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if queries != before {
		t.Errorf("%d further queries for names already stored", queries-before)
	}
}
