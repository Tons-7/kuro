package store

import (
	"context"
	"testing"
)

// An adopted download and a later playback write two file rows for one torrent,
// which joined plainly would list the same download twice.
func TestDownloadStatusListsEachTorrentOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 154587, "FINISHED", 28)

	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO torrent (info_hash, rqbit_id, name, total_bytes, state, added_at)
		VALUES ('abc', 1, '[Erai-raws] Sousou no Frieren - 12.mkv', 100, 'live', 1)`); err != nil {
		t.Fatal(err)
	}
	// What adoption writes: a file with no anime.
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO torrent_file (info_hash, file_index, path, size_bytes, selected)
		VALUES ('abc', 1, 'other.mkv', 100, 1)`); err != nil {
		t.Fatal(err)
	}
	// What playback writes: the same torrent, linked to its episode.
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO torrent_file (info_hash, file_index, path, size_bytes, anime_id, ep_key, selected)
		VALUES ('abc', 0, 'frieren12.mkv', 100, 154587, '12', 1)`); err != nil {
		t.Fatal(err)
	}

	items, err := s.DownloadStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d rows, want 1 per torrent", len(items))
	}
	// The row that knows the show is the one worth showing.
	if items[0].AnimeID != 154587 || items[0].Episode != "12" {
		t.Errorf("got anime %d episode %q, want the linked file",
			items[0].AnimeID, items[0].Episode)
	}
	if items[0].Title == "" {
		t.Error("the row should carry the show's title, not just the filename")
	}
}

// A release superseded by another for the same episode keeps its torrent row but
// has its file deselected; it must not linger as an empty duplicate.
func TestDownloadStatusHidesSupersededRelease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 154587, "FINISHED", 28)

	// The chosen release for episode 12.
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO torrent (info_hash, rqbit_id, name, total_bytes, state, added_at)
		VALUES ('chosen', 1, 'Frieren - 12 [good].mkv', 100, 'live', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO torrent_file (info_hash, file_index, path, size_bytes, anime_id, ep_key, selected)
		VALUES ('chosen', 0, 'good.mkv', 100, 154587, '12', 1)`); err != nil {
		t.Fatal(err)
	}
	// A second release for the same episode, deselected in favour of the chosen one.
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO torrent (info_hash, rqbit_id, name, total_bytes, state, added_at)
		VALUES ('superseded', 2, 'Frieren - 12 [other].mkv', 100, 'live', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO torrent_file (info_hash, file_index, path, size_bytes, anime_id, ep_key, selected)
		VALUES ('superseded', 0, 'other.mkv', 100, 154587, '12', 0)`); err != nil {
		t.Fatal(err)
	}

	items, err := s.DownloadStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d rows, want only the chosen release", len(items))
	}
	if items[0].InfoHash != "chosen" {
		t.Errorf("listed %q, want the chosen release", items[0].InfoHash)
	}
}

// A season pack serves several episodes from one torrent. The row must name
// all of them, in order, and count as playing while any of them is pinned.
func TestDownloadStatusFoldsASeasonPack(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 154587, "FINISHED", 28)

	for _, r := range []TorrentRecord{
		{InfoHash: "pack", RqbitID: 1, Name: "Frieren BD", TotalSize: 100, AnimeID: 154587, EpKey: "5", FileIndex: 7, FilePath: "05.mkv"},
		{InfoHash: "pack", RqbitID: 1, Name: "Frieren BD", TotalSize: 100, AnimeID: 154587, EpKey: "3", FileIndex: 2, FilePath: "03.mkv"},
		{InfoHash: "pack", RqbitID: 1, Name: "Frieren BD", TotalSize: 100, AnimeID: 154587, EpKey: "12", FileIndex: 9, FilePath: "12.mkv"},
	} {
		if err := s.RecordTorrent(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	s.SetCacheBytes(ctx, "pack", 2, 60, true)
	s.SetCacheBytes(ctx, "pack", 7, 60, true)
	if err := s.PinOnly(ctx, "pack", 9); err != nil {
		t.Fatal(err)
	}
	s.KeepDownload(ctx, "pack", true)

	items, err := s.DownloadStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d rows, want 1", len(items))
	}
	d := items[0]
	if d.Episode != "3, 5, 12" || len(d.Episodes) != 3 || d.Episodes[2] != "12" {
		t.Errorf("episodes = %q %v, want numeric order", d.Episode, d.Episodes)
	}
	if !d.Pinned || !d.Kept {
		t.Errorf("pinned=%v kept=%v, want both from the pack's files", d.Pinned, d.Kept)
	}
	// Three 100-byte files: the pack's total is their sum, not one file's size.
	if d.TotalSize != 300 || d.OnDisk != 120 || d.Percent != 40 {
		t.Errorf("total=%d onDisk=%d percent=%v", d.TotalSize, d.OnDisk, d.Percent)
	}
	if d.AnimeID != 154587 || d.Title == "" {
		t.Errorf("anime=%d title=%q", d.AnimeID, d.Title)
	}
}

func TestDownloadStatusEpisodesIsNeverNull(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.TrackTorrent(ctx, "adopted", 3, "something.mkv"); err != nil {
		t.Fatal(err)
	}
	items, err := s.DownloadStatus(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if items[0].Episodes == nil || len(items[0].Episodes) != 0 || items[0].Episode != "" {
		t.Fatalf("episodes = %#v", items[0].Episodes)
	}
}
