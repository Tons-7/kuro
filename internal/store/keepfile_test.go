package store

import (
	"context"
	"testing"
)

const packHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func packStore(t *testing.T) *Store {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.ImportList(ctx, []Anime{
		{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]"},
	}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	for i, ep := range []string{"1", "2", "3"} {
		if err := st.RecordTorrent(ctx, TorrentRecord{
			InfoHash: packHash, Name: "[Group] Show (01-03) [Batch]", AnimeID: 1, EpKey: ep,
			FileIndex: i, FilePath: "ep" + ep + ".mkv", TotalSize: 300,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// Keeping one episode of a pack must not keep the other two, and the pack row
// reads as kept while any of them is.
func TestKeepFileIsPerEpisode(t *testing.T) {
	st := packStore(t)
	ctx := context.Background()

	found, err := st.KeepFile(ctx, packHash, 1, true)
	if err != nil || !found {
		t.Fatalf("keep file: found=%v err=%v", found, err)
	}

	files, err := st.DownloadFiles(ctx, packHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %+v, want the three episodes", files)
	}
	for _, f := range files {
		if f.Kept != (f.FileIndex == 1) {
			t.Errorf("file %d kept=%v", f.FileIndex, f.Kept)
		}
	}
	if files[0].EpKey != "1" || files[2].EpKey != "3" {
		t.Errorf("files are not in episode order: %+v", files)
	}

	rows, err := st.DownloadStatus(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("status = %+v, %v", rows, err)
	}
	if !rows[0].Kept {
		t.Error("the pack row should read as kept while an episode is")
	}

	if _, err := st.KeepFile(ctx, packHash, 1, false); err != nil {
		t.Fatal(err)
	}
	rows, _ = st.DownloadStatus(ctx)
	if rows[0].Kept {
		t.Error("nothing kept any more, but the row still says so")
	}
}

func TestKeepFileReportsAMissingFile(t *testing.T) {
	st := packStore(t)
	found, err := st.KeepFile(context.Background(), packHash, 9, true)
	if err != nil || found {
		t.Fatalf("found=%v err=%v, want not found", found, err)
	}
}

// The pack row's own toggle still moves everything at once.
func TestKeepDownloadStillMovesTheWholePack(t *testing.T) {
	st := packStore(t)
	ctx := context.Background()
	if _, err := st.KeepDownload(ctx, packHash, true); err != nil {
		t.Fatal(err)
	}
	files, _ := st.DownloadFiles(ctx, packHash)
	for _, f := range files {
		if !f.Kept {
			t.Errorf("file %d not kept", f.FileIndex)
		}
	}
}
