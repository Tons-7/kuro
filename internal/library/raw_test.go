package library

import (
	"errors"
	"testing"

	"kuro/internal/indexer"
	"kuro/internal/parse"
	"kuro/internal/score"
)

func rawResult(title string) score.Result {
	c := score.Candidate{
		Torrent: indexer.Torrent{Title: title, Seeders: 40, Category: "1_4 Raw"},
		Release: parse.Parse(title),
	}
	return score.Rank([]score.Candidate{c}, score.DefaultPreferences())[0]
}

// A just-aired episode is normally raw for a few hours. Reporting that as
// "nothing found" reads as a failure when the truth is "not yet".
func TestNoReleaseSinglesOutARawOnlyEpisode(t *testing.T) {
	raw := rawResult("[Ohys-Raws] Show - 07 (TX 1280x720 x264 AAC).mp4")
	if !raw.RejectedOnlyAsRaw() {
		t.Fatalf("expected a raw rejection, got %v", raw.Rejections)
	}

	err := noRelease(Candidates{Results: []score.Result{raw}, Queries: []string{"a"}}, PlayRequest{Episode: 7})

	var missing *NoRelease
	if !errors.As(err, &missing) {
		t.Fatalf("error is not a *NoRelease: %T", err)
	}
	if !missing.RawOnly {
		t.Fatal("a raw-only episode was not flagged")
	}
	if missing.RawTitle == "" {
		t.Error("no raw release named for the user")
	}
}

func TestNoReleaseStaysGenericWhenNothingMatched(t *testing.T) {
	err := noRelease(Candidates{Queries: []string{"a", "b"}}, PlayRequest{Episode: 7})

	var missing *NoRelease
	if !errors.As(err, &missing) {
		t.Fatalf("error is not a *NoRelease: %T", err)
	}
	if missing.RawOnly {
		t.Fatal("nothing was found, so nothing can be raw")
	}
}

// Asking for the raw explicitly has to actually return it.
func TestAllowRawAcceptsTheRelease(t *testing.T) {
	c := score.Candidate{
		Torrent: indexer.Torrent{
			Title:   "[Ohys-Raws] Show - 07 (TX 1280x720 x264 AAC).mp4",
			Seeders: 40, Category: "1_4 Raw",
		},
		Release: parse.Parse("[Ohys-Raws] Show - 07 (TX 1280x720 x264 AAC).mp4"),
	}

	prefs := score.DefaultPreferences()
	prefs.AllowRaw = true
	if got := score.Rank([]score.Candidate{c}, prefs)[0]; !got.AutoPick {
		t.Fatalf("raw still blocked with AllowRaw: %q", got.Blocked)
	}
}
