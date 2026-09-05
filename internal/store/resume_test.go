package store

import (
	"context"
	"testing"
)

// Reaching the end of the bar is not the same as watching the episode: the
// position has to get there and enough of it has to have actually played.
func TestPeekAtTheEndingIsNotWatched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "FINISHED", 12)

	if playThrough(t, s, 1, "2", 0, 300, 1435) {
		t.Fatal("five minutes in is not watched")
	}

	// A tap at the end of the bar, a second of the credits, pause.
	watched, err := s.SavePlayback(ctx, PlaybackState{
		AnimeID: 1, EpKey: "2", Position: 1420, Duration: 1435, Played: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if watched {
		t.Error("a seek to the end counted as watching the episode")
	}

	// The peek must not have replaced the resume point either.
	at, err := s.ResumeAt(ctx, 1, "2")
	if err != nil {
		t.Fatal(err)
	}
	if at != 300 {
		t.Errorf("resume at %v, want the 300s the viewer actually reached", at)
	}

	p, ok, err := s.LastInProgress(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("expected an episode in progress, ok=%v err=%v", ok, err)
	}
	if p.EpKey != "2" || p.Position != 300 {
		t.Errorf("in progress = %+v", p)
	}
}

// Playing through normally still marks the episode, and a finished episode
// starts over rather than resuming in the credits.
func TestPlayingThroughIsWatchedAndStartsOver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "FINISHED", 12)

	if !playThrough(t, s, 1, "2", 0, 1435, 1435) {
		t.Fatal("an episode played end to end is watched")
	}
	at, err := s.ResumeAt(ctx, 1, "2")
	if err != nil {
		t.Fatal(err)
	}
	if at != 0 {
		t.Errorf("a finished episode should start over, got %v", at)
	}
	if _, ok, _ := s.LastInProgress(ctx, 1); ok {
		t.Error("a finished episode is not in progress")
	}
}

// Skipping the opening and the ending is still watching the episode.
func TestSkippingOpeningAndEndingIsWatched(t *testing.T) {
	s := newTestStore(t)
	seedCatalogue(t, s, 1, "FINISHED", 12)

	playThrough(t, s, 1, "3", 0, 10, 1435)
	// Skip the opening: a jump, nothing played.
	if _, err := s.SavePlayback(context.Background(), PlaybackState{
		AnimeID: 1, EpKey: "3", Position: 100, Duration: 1435,
	}); err != nil {
		t.Fatal(err)
	}
	if playThrough(t, s, 1, "3", 100, 1290, 1435) {
		t.Fatal("watched before the threshold")
	}
	// Skip the ending.
	watched, err := s.SavePlayback(context.Background(), PlaybackState{
		AnimeID: 1, EpKey: "3", Position: 1400, Duration: 1435,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !watched {
		t.Error("skipping the opening and ending should still count as watched")
	}
}

// Winding a watched episode back to its middle resumes there and puts it back on
// the continue rail, flagged resumable in the episode list.
func TestWatchedEpisodeWoundBackResumesThere(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "FINISHED", 12)

	playThrough(t, s, 1, "2", 0, 1435, 1435)
	if _, err := s.SavePlayback(ctx, PlaybackState{
		AnimeID: 1, EpKey: "2", Position: 1127, Duration: 1435,
	}); err != nil {
		t.Fatal(err)
	}

	at, err := s.ResumeAt(ctx, 1, "2")
	if err != nil {
		t.Fatal(err)
	}
	if at != 1127 {
		t.Errorf("resume at %v, want 1127", at)
	}

	page, err := s.ContinueWatching(ctx, Paging{PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Resume == nil || page.Items[0].Resume.Position != 1127 {
		t.Errorf("continue watching should offer the wound-back episode: %+v", page.Items)
	}

	eps, err := s.Episodes(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eps {
		if e.EpKey == "2" && !e.Resumable {
			t.Error("episode 2 holds a position worth resuming but is not flagged resumable")
		}
	}
}

// The first report of an episode is accepted wherever it lands — there is no
// earlier resume point to protect.
func TestFirstReportNearTheEndIsKept(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "FINISHED", 12)

	if _, err := s.SavePlayback(ctx, PlaybackState{
		AnimeID: 1, EpKey: "5", Position: 1400, Duration: 1435, Played: 3,
	}); err != nil {
		t.Fatal(err)
	}
	at, err := s.ResumeAt(ctx, 1, "5")
	if err != nil {
		t.Fatal(err)
	}
	if at != 0 {
		t.Errorf("resume at %v; the credits are not a resume point", at)
	}
}
