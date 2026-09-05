package metadata

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// ani.zip keys episodes by the entry's own count; episodeNumber is TheTVDB's
// position in its season, which for a later cour starts well past one.
func TestEpisodesCountFromTheEntryAndKeepTheTvdbNumber(t *testing.T) {
	c := testClient(t, "anizip", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"episodes": {
		  "1": {"episode":"1","episodeNumber":41,"seasonNumber":17,"absoluteEpisodeNumber":407},
		  "2": {"episode":"2","episodeNumber":42,"seasonNumber":17,"absoluteEpisodeNumber":408}
		}}`)
	})

	got, err := c.Episodes(context.Background(), 185874)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Episodes) != 2 {
		t.Fatalf("got %d episodes, want 2", len(got.Episodes))
	}
	for _, e := range got.Episodes {
		if e.TvdbNumber != e.Number+40 || e.Absolute != e.Number+406 || e.Number > 2 {
			t.Errorf("episode = %+v, want number n, TVDB n+40, absolute n+406", e)
		}
	}
}
