package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// A mirror that has no record yet answers 404; the other host is asked before the show reads as not found.
func TestAnimeAsksJikanWhenTenraiHasNoRecord(t *testing.T) {
	c := New()
	c.SetURL("tenrai-anime", serve(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	c.SetURL("jikan-anime", serve(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"mal_id":61000,"title":"Just added"}}`)
	}))

	got, err := c.Anime(context.Background(), 61000)
	if err != nil || got.Romaji != "Just added" {
		t.Fatalf("got %+v, err %v; want Jikan's record", got, err)
	}
}

// Both hosts answering with nothing is a show that does not exist, said once rather than retried.
func TestAnimeNotFoundWhenNoHostHasIt(t *testing.T) {
	asked := 0
	missing := func(w http.ResponseWriter, r *http.Request) { asked++; http.NotFound(w, r) }
	c := New()
	c.SetURL("tenrai-anime", serve(t, missing))
	c.SetURL("jikan-anime", serve(t, missing))

	if _, err := c.Anime(context.Background(), 5); err == nil {
		t.Fatal("want not found")
	}
	if asked != 2 {
		t.Fatalf("%d requests; an answer of nothing from both is not worth retrying", asked)
	}
}

// A Tenrai that hangs gets its own deadline, so Jikan is still asked inside the caller's budget.
func TestAnimeStalledTenraiLeavesTimeForJikan(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	c := New()
	c.SetURL("tenrai-anime", serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	c.SetURL("jikan-anime", serve(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"mal_id":9,"title":"Found"}}`)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	got, err := c.Anime(ctx, 9)
	if err != nil || got.Romaji != "Found" {
		t.Fatalf("got %+v, err %v; a stalled first host should not use up the budget", got, err)
	}
}

const fullJSON = `{"data":{"mal_id":1,"theme":{"openings":["1: \"Yuusha\" by YOASOBI (eps 1-16)"]},"trailer":{"url":"t"}}}`

const staffJSON = `{"data":[{"person":{"name":"Keiichirou Saitou"},"positions":["Director"]}]}`

// A failed staff request is a partial answer, flagged so it is not kept as a show without staff.
func TestExtraFlagsAMissingStaffHalf(t *testing.T) {
	c := New()
	c.SetURL("tenrai-full", serve(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fullJSON) }))
	down := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", http.StatusBadGateway) })
	c.SetURL("tenrai-staff", down)
	c.SetURL("jikan-staff", down)

	got, err := c.Extra(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Openings) != 1 || !got.StaffMissing {
		t.Fatalf("got %+v; want the themes, flagged as missing its staff", got)
	}
}

// Extra falls back to Jikan as Anime does, which serves the same shapes.
func TestExtraFallsBackToJikan(t *testing.T) {
	c := New()
	down := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", http.StatusGatewayTimeout) })
	c.SetURL("tenrai-full", down)
	c.SetURL("tenrai-staff", down)
	c.SetURL("jikan-full", serve(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fullJSON) }))
	c.SetURL("jikan-staff", serve(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, staffJSON) }))

	got, err := c.Extra(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trailer != "t" || len(got.Staff) != 1 || got.StaffMissing {
		t.Fatalf("got %+v; want Jikan's themes and staff", got)
	}
}
