package library

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"kuro/internal/db"
	"kuro/internal/metadata"
	"kuro/internal/store"
	"kuro/internal/transcode"
)

func skipStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)
	ctx := context.Background()
	if _, err := conn.W.ExecContext(ctx,
		`INSERT INTO anime (id, mal_id, title_romaji, synced_at) VALUES (7, 52991, 'Frieren', 0)`); err != nil {
		t.Fatal(err)
	}
	return st
}

type fakeProber struct{ info *transcode.MediaInfo }

func (f fakeProber) Probe(context.Context, string) (*transcode.MediaInfo, error) {
	return f.info, nil
}

// mpv auto-skips only from what it is handed, so the launch path has to resolve
// the ranges itself rather than rely on the browser having played the episode.
func TestMpvSkipRangesPreferChaptersAndFallBack(t *testing.T) {
	st := skipStore(t)
	ctx := context.Background()
	req := PlayRequest{AnimeID: 7, Episode: 1}

	both := fakeProber{info: &transcode.MediaInfo{Duration: 1470, Chapters: []transcode.Chapter{
		{Title: "Intro", Start: 90, End: 180, Kind: "op"},
		{Title: "Credits", Start: 1350, End: 1440, Kind: "ed"},
	}}}
	p := NewPlayback(st, nil, nil, nil, t.TempDir(), slog.New(slog.DiscardHandler)).WithProber(both)

	got := p.skipRanges(ctx, req, "x.mkv")
	if len(got) != 2 || got[0].Kind != "op" || got[0].Start != 90 || got[1].Kind != "ed" {
		t.Fatalf("chapters not used: %+v", got)
	}

	// Only the opening is marked; the ending has to come from what was stored.
	if err := st.SaveSkips(ctx, 52991, 1, []metadata.SkipTime{
		{Kind: "ed", Start: 1300, End: 1390},
	}); err != nil {
		t.Fatal(err)
	}
	opOnly := fakeProber{info: &transcode.MediaInfo{Duration: 1470, Chapters: []transcode.Chapter{
		{Title: "Intro", Start: 90, End: 180, Kind: "op"},
	}}}
	p = NewPlayback(st, nil, nil, nil, t.TempDir(), slog.New(slog.DiscardHandler)).WithProber(opOnly)

	got = p.skipRanges(ctx, req, "x.mkv")
	if len(got) != 2 {
		t.Fatalf("got %+v, want the chapter opening and the stored ending", got)
	}
	if !hasKind(got, "op") || !hasKind(got, "ed") {
		t.Errorf("kinds = %+v", got)
	}
	for _, r := range got {
		if r.Kind == "op" && r.Start != 90 {
			t.Errorf("the chapter opening was replaced: %+v", r)
		}
	}
}

// AniSkip matches on the episode's length, so a guessed one returns either
// nothing or another cut's timings. Without a real length, do not ask.
func TestSkipsWithoutADurationDoesNotAsk(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"found":false,"results":[]}`)
	}))
	t.Cleanup(srv.Close)

	meta := metadata.New()
	meta.SetURL("aniskip", srv.URL)
	e := NewEnricher(skipStore(t), meta, slog.New(slog.DiscardHandler))

	if err := e.Skips(context.Background(), 7, 1, 0); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("made %d requests with no duration, want none", got)
	}

	if err := e.Skips(context.Background(), 7, 1, 1470); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d requests with a real duration, want 1", got)
	}
}
