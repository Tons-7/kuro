package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/db"
	"kuro/internal/store"
)

// AniList answers 50 ids at a time and says nothing about the rest, so a
// franchise of 61 films used to come back with 11 rows still bare.
func TestHydratePagesPastFiftyIds(t *testing.T) {
	var asked [][]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				IDs []int `json:"ids"`
			} `json:"variables"`
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		asked = append(asked, req.Variables.IDs)

		var media []string
		for _, id := range req.Variables.IDs {
			media = append(media, fmt.Sprintf(
				`{"id":%d,"title":{"romaji":"Show %d"},"synonyms":[],"genres":[],"studios":{"nodes":[]}}`, id, id))
		}
		io.WriteString(w, `{"data":{"Page":{"media":[`+strings.Join(media, ",")+`]}}}`)
	}))
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}

	ids := make([]int, 0, 61)
	for i := 1; i <= 61; i++ {
		ids = append(ids, 1000+i)
	}

	st := store.New(conn)
	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	saved, err := NewImporter(st, al, discard()).Hydrate(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if saved != len(ids) {
		t.Errorf("saved %d of %d", saved, len(ids))
	}
	if len(asked) != 2 || len(asked[0]) != 50 || len(asked[1]) != 11 {
		t.Fatalf("batches = %v", batchSizes(asked))
	}

	for _, id := range []int{1001, 1050, 1061} {
		names, err := st.SearchTitles(context.Background(), id)
		if err != nil || len(names) == 0 {
			t.Errorf("%d has no title: %v", id, err)
		}
	}
}

func batchSizes(batches [][]int) []int {
	out := make([]int, 0, len(batches))
	for _, b := range batches {
		out = append(out, len(b))
	}
	return out
}
