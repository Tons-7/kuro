package store

import (
	"context"
	"testing"
)

// torrent_file.anime_id is a foreign key; playing a show the catalogue never
// imported must still link the download, not fail silently and leave it orphaned.
func TestRecordTorrentForAnUnknownAnime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.RecordTorrent(ctx, TorrentRecord{
		InfoHash:  "abc123",
		RqbitID:   4,
		Name:      "[Erai-raws] Death Note - 01 ~ 37",
		TotalSize: 900,
		AnimeID:   1535,
		EpKey:     "1",
		FileIndex: 0,
		FilePath:  "Death Note/ep01.mkv",
	})
	if err != nil {
		t.Fatalf("recording a torrent for an unimported anime: %v", err)
	}

	got, found, err := s.TorrentForEpisode(ctx, 1535, "1")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("the download was not linked to its episode")
	}
	if got.InfoHash != "abc123" || got.FileIndex != 0 {
		t.Errorf("got %+v, want the recorded torrent", got)
	}
	if got.FilePath != "Death Note/ep01.mkv" {
		t.Errorf("path = %q, want the file inside the pack", got.FilePath)
	}
}
