package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestAnswersRoundTripAndPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	body := []byte(`{"Page":{"media":[{"id":1}]}}`)
	if err := s.SaveAnswer(ctx, "k1", body); err != nil {
		t.Fatal(err)
	}
	got, at, ok := s.LoadAnswer(ctx, "k1")
	if !ok || string(got) != string(body) || time.Since(at) > time.Minute {
		t.Fatalf("got %q at %v ok=%v", got, at, ok)
	}
	if _, _, ok := s.LoadAnswer(ctx, "missing"); ok {
		t.Fatal("a missing answer loaded")
	}

	if err := s.SaveAnswer(ctx, "k2", body); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `UPDATE http_cache SET fetched_at = 1 WHERE url = 'k1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneAnswers(ctx, time.Now().Add(-time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.LoadAnswer(ctx, "k1"); ok {
		t.Error("an answer past its age was kept")
	}
	if _, _, ok := s.LoadAnswer(ctx, "k2"); !ok {
		t.Error("a recent answer was pruned")
	}
}

func TestAnimeRecordSkipsStubs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	status, english := "RELEASING", "Frieren"
	next, at := 5, int(time.Now().Add(time.Hour).Unix())
	if _, err := s.ImportList(ctx, []Anime{{
		ID: 7, Romaji: "Sousou no Frieren", English: &english, Status: &status,
		Genres: `["Fantasy"]`, Studios: `["Madhouse"]`, StartDate: "2023-09-29",
		NextEpisode: &next, NextAiringAt: &at,
	}}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	a, synced, ok, err := s.AnimeRecord(ctx, 7)
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}
	if a.Romaji != "Sousou no Frieren" || *a.English != english || a.Studios != `["Madhouse"]` ||
		a.StartDate != "2023-09-29" || *a.NextEpisode != 5 || time.Since(synced) > time.Minute {
		t.Fatalf("record read back wrong: %+v", a)
	}

	if _, err := s.w.ExecContext(ctx, `INSERT INTO anime (id, title_romaji, synced_at) VALUES (8, 'Unknown', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.AnimeRecord(ctx, 8); ok {
		t.Error("a stub row passed for a record")
	}
}

func TestStaleAiringPicksShowsPastDue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	for _, row := range []struct {
		id             int
		status         string
		nextAt, synced int64
	}{
		{1, "RELEASING", now - 600, now - 3600},            // aired since last looked: due
		{2, "RELEASING", now + 3600, now - 600},            // checked recently: not due
		{3, "RELEASING", now + 3600, now - 7*3600},         // older than six hours: due
		{4, "NOT_YET_RELEASED", now + 86400, now - 7*3600}, // announced, under a day: not due
		{5, "NOT_YET_RELEASED", now + 86400, now - 25*3600},
		{6, "FINISHED", 0, now - 90*86400},    // over: never
		{7, "RELEASING", now - 600, now - 60}, // looked after it aired: not due
	} {
		var nextAt any
		if row.nextAt != 0 {
			nextAt = row.nextAt
		}
		if _, err := s.w.ExecContext(ctx,
			`INSERT INTO anime (id, title_romaji, status, next_airing_at, synced_at) VALUES (?, 'x', ?, ?, ?)`,
			row.id, row.status, nextAt, row.synced); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := s.StaleAiring(ctx, time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int{1, 3, 5}) {
		t.Fatalf("due = %v, want [1 3 5]", ids)
	}
	if first, _ := s.StaleAiring(ctx, time.Now(), 1); !slices.Equal(first, []int{1}) {
		t.Errorf("a passed broadcast should come first, got %v", first)
	}
}

func TestNotInCorpusAndRevive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.SaveCorpus(ctx, []CorpusEntry{{AniListID: 1, Titles: []CorpusTitle{{Text: "One", Kind: "primary"}}}}); err != nil {
		t.Fatal(err)
	}
	missing, err := s.NotInCorpus(ctx, []int{1, 2})
	if err != nil || !slices.Equal(missing, []int{2}) {
		t.Fatalf("missing = %v, %v", missing, err)
	}

	if err := s.MarkDead(ctx, []int{2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Revive(ctx, []int{2}); err != nil {
		t.Fatal(err)
	}
	known, _ := s.KnownIDs(ctx)
	if _, dead := known[2]; dead {
		t.Error("revived id still marked dead")
	}
}
