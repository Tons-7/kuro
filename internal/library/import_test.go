package library

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/db"
	"kuro/internal/store"
)

func ptr[T any](v T) *T { return &v }

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestToAnimePrefersRomajiThenUserPreferred(t *testing.T) {
	tests := []struct {
		name  string
		title anilist.Title
		want  string
	}{
		{"romaji", anilist.Title{Romaji: ptr("Sousou no Frieren"), UserPreferred: ptr("Frieren")}, "Sousou no Frieren"},
		{"falls back", anilist.Title{UserPreferred: ptr("Frieren")}, "Frieren"},
		{"neither", anilist.Title{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toAnime(anilist.Media{Title: tt.title}).Romaji; got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Nil slices must serialise as [] because the columns are NOT NULL.
func TestToAnimeSerialisesEmptyArrays(t *testing.T) {
	a := toAnime(anilist.Media{})
	if a.Synonyms != "[]" || a.Genres != "[]" {
		t.Fatalf("synonyms=%q genres=%q", a.Synonyms, a.Genres)
	}

	b := toAnime(anilist.Media{Genres: []string{"Action", "Drama"}})
	if b.Genres != `["Action","Drama"]` {
		t.Fatalf("genres = %q", b.Genres)
	}
}

func TestToAnimeCarriesNullsThrough(t *testing.T) {
	a := toAnime(anilist.Media{ID: 100})
	if a.Episodes != nil || a.NextEpisode != nil || a.MalID != nil {
		t.Fatal("absent fields should stay nil rather than becoming zero")
	}

	withAiring := toAnime(anilist.Media{
		ID:         100,
		NextAiring: &anilist.Airing{Episode: 9, AiringAt: 1786000000},
	})
	if withAiring.NextEpisode == nil || *withAiring.NextEpisode != 9 {
		t.Fatal("next episode not mapped")
	}
	if withAiring.NextAiringAt == nil || *withAiring.NextAiringAt != 1786000000 {
		t.Fatal("airing timestamp not mapped")
	}
}

func TestToEntryFormatsFuzzyDates(t *testing.T) {
	e := toEntry(anilist.ListEntry{
		ID: 1, MediaID: 100, Progress: 7,
		StartedAt:   anilist.FuzzyDate{Year: ptr(2026), Month: ptr(1), Day: ptr(5)},
		CompletedAt: anilist.FuzzyDate{},
	})

	if e.StartedAt != "2026-01-05" {
		t.Fatalf("startedAt = %q", e.StartedAt)
	}
	if e.CompletedAt != "" {
		t.Fatalf("an unset date should map to empty, got %q", e.CompletedAt)
	}
}

func TestRunImportsDeduplicatedEntries(t *testing.T) {
	const body = `{"data":{"MediaListCollection":{"lists":[
      {"name":"Watching","entries":[
        {"id":1,"mediaId":100,"progress":7,"status":"CURRENT",
         "media":{"id":100,"title":{"romaji":"Frieren"},"episodes":28}}]},
      {"name":"Fav","isCustomList":true,"entries":[
        {"id":1,"mediaId":100,"progress":7,"status":"CURRENT",
         "media":{"id":100,"title":{"romaji":"Frieren"},"episodes":28}}]}]}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}

	st := store.New(conn)
	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())

	res, err := NewImporter(st, al, discard()).Run(context.Background(), 1, store.ImportMerge)
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries != 1 {
		t.Fatalf("entries = %d, want 1 after dedupe", res.Entries)
	}

	page, err := st.Library(context.Background(), store.LibraryFilter{}, store.Paging{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Romaji != "Frieren" || page.Items[0].Progress != 7 {
		t.Fatalf("library = %+v", page.Items)
	}
}

func TestRunPropagatesAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"nope","status":401}]}`)
	}))
	defer srv.Close()

	conn, _ := db.Open(filepath.Join(t.TempDir(), "test.db"))
	defer conn.Close()
	conn.Migrate()

	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	if _, err := NewImporter(store.New(conn), al, discard()).Run(context.Background(), 1, store.ImportMerge); err == nil {
		t.Fatal("expected the API error to surface")
	}
}
