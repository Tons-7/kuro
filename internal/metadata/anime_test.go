package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Jikan answers 504 for exactly the obscure MAL-only titles this is for, so
// Tenrai leads and Jikan is the second chance. Neither path touches AniList.
func TestAnimeFallsBackWhenTenraiIsDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
	}))
	t.Cleanup(dead.Close)

	jikan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"mal_id":208685,"title":"A MAL-only show","episodes":8}}`)
	}))
	t.Cleanup(jikan.Close)

	c := New()
	c.SetURL("tenrai-anime", dead.URL)
	c.SetURL("jikan-anime", jikan.URL)

	got, err := c.Anime(context.Background(), 208685)
	if err != nil {
		t.Fatal(err)
	}
	if got.MalID != 208685 || got.Romaji != "A MAL-only show" {
		t.Fatalf("got %+v", got)
	}
}
