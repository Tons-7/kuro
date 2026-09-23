package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/corpus"
	"kuro/internal/db"
	"kuro/internal/store"
)

// A failed title fetch must leave discovery due, or the new ids wait a day.
func TestDiscoveryStaysDueWhenTitlesFail(t *testing.T) {
	var aniUp atomic.Bool
	ani := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !aniUp.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `{"data":{"Page":{"media":[{"id":5,"title":{"romaji":"New Show"}}]}}}`)
	}))
	t.Cleanup(ani.Close)
	sources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/animeapi":
			io.WriteString(w, "anilist\tmyanimelist\n5\t0\n")
		default:
			http.Error(w, "gone", http.StatusNotFound)
		}
	}))
	t.Cleanup(sources.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)

	f := corpus.NewFetcher()
	f.SetURL("manami", sources.URL+"/manami")
	f.SetURL("animeapi", sources.URL+"/animeapi")
	al := anilist.New(discard(), anilist.WithEndpoint(ani.URL), anilist.WithoutRateLimit())
	ing := NewIngester(st, f, al, discard())
	ctx := context.Background()

	// The seed fails too; discovery must still run.
	rep, err := ing.Run(ctx, false)
	if err == nil {
		t.Fatal("want an error from the failed seed and fetch")
	}
	if rep.Discovered != 1 {
		t.Fatalf("discovered = %d, want 1 despite the failed seed", rep.Discovered)
	}
	if at, _ := st.SourceRefreshedAt(ctx, "animeapi"); !at.IsZero() {
		t.Fatal("animeapi marked fresh though its titles never arrived")
	}

	aniUp.Store(true)
	rep, _ = ing.Run(ctx, false)
	if rep.Fetched != 1 {
		t.Fatalf("retry fetched %d, want 1", rep.Fetched)
	}
	if at, _ := st.SourceRefreshedAt(ctx, "animeapi"); at.IsZero() {
		t.Error("animeapi not marked after a complete run")
	}
}
