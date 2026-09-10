package library

import (
	"errors"
	"strings"
	"testing"
	"time"

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

	err := noRelease(Candidates{Results: []score.Result{raw}, Queries: []string{"a"}}, PlayRequest{Episode: 7}, true, 0)

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
	err := noRelease(Candidates{Queries: []string{"a", "b"}}, PlayRequest{Episode: 7}, true, 0)

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

// An episode the catalogue puts in the future is not a search failure, and
// saying "none met your preferences" sends people to change settings for nothing.
func TestNoReleaseSaysWhenTheEpisodeIsNotOut(t *testing.T) {
	airs := time.Now().Add(50 * time.Hour).Unix()
	err := noRelease(Candidates{Queries: []string{"a"}}, PlayRequest{Episode: 8}, false, airs)

	var missing *NoRelease
	if !errors.As(err, &missing) {
		t.Fatalf("error is not a *NoRelease: %T", err)
	}
	if !missing.Unaired {
		t.Error("the episode was not flagged as unaired")
	}
	if !strings.Contains(err.Error(), "has not aired yet") {
		t.Errorf("message = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "2 days") {
		t.Errorf("message does not say when it airs: %q", err.Error())
	}
}
