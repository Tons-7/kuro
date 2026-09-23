package library

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/db"
	"kuro/internal/store"
)

func rememberStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	return store.New(conn)
}

func sampleMedia(id int) anilist.Media {
	romaji, english, status, format := "Sousou no Frieren", "Frieren", "RELEASING", "TV"
	episodes, year, mal := 28, 2023, 52991
	return anilist.Media{
		ID: id, IDMal: &mal, Status: &status, Format: &format, Episodes: &episodes, SeasonYear: &year,
		Title:  anilist.Title{Romaji: &romaji, English: &english},
		Genres: []string{"Fantasy", "Drama"},
		Studios: anilist.Studios{Nodes: []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}{{ID: 11, Name: "Madhouse"}}},
		StartDate:  anilist.FuzzyDate{Year: &year},
		NextAiring: &anilist.Airing{Episode: 12, AiringAt: int(time.Now().Add(48 * time.Hour).Unix())},
	}
}

// A show first seen in Browse is kept, and joins the corpus so it can be matched.
func TestRememberKeepsShowAndAddsItToCorpus(t *testing.T) {
	st := rememberStore(t)
	imp := NewImporter(st, nil, discard())
	added := make(chan struct{}, 1)
	imp.OnNewTitles = func() { added <- struct{}{} }

	page, _ := anilist.WithServed(context.Background())
	imp.Remember(page, []anilist.Media{sampleMedia(154587)})
	select {
	case <-added:
	case <-time.After(5 * time.Second):
		t.Fatal("new show never reached the corpus")
	}

	a, _, ok, err := st.AnimeRecord(context.Background(), 154587)
	if err != nil || !ok || *a.English != "Frieren" {
		t.Fatalf("record: %+v ok=%v err=%v", a, ok, err)
	}
	if missing, _ := st.NotInCorpus(context.Background(), []int{154587}); len(missing) != 0 {
		t.Error("not in the corpus")
	}

	// Seen again inside ten minutes: nothing newer to write.
	imp.Remember(page, []anilist.Media{sampleMedia(154587)})
	select {
	case <-added:
		t.Error("written again within the window")
	case <-time.After(200 * time.Millisecond):
	}
}

// An answer from the saved copy is old: storing it would stamp it current.
func TestSavedAnswersAreNeverWrittenBack(t *testing.T) {
	st := rememberStore(t)
	imp := NewImporter(st, nil, discard())

	page, _ := anilist.WithServed(context.Background())
	anilist.NoteSaved(page, time.Now().Add(-48*time.Hour))

	imp.Remember(page, []anilist.Media{sampleMedia(1)})
	if n, err := imp.Save(page, []anilist.Media{sampleMedia(2)}); n != 0 || err != nil {
		t.Fatalf("Save wrote %d, %v", n, err)
	}
	time.Sleep(100 * time.Millisecond)
	for _, id := range []int{1, 2} {
		if _, _, ok, _ := st.AnimeRecord(context.Background(), id); ok {
			t.Errorf("anime %d written from a saved answer", id)
		}
	}
}

// The saved row read back reads as what AniList sent, bar studio ids.
func TestFromRecordRoundTrips(t *testing.T) {
	st := rememberStore(t)
	m := sampleMedia(9)
	if _, err := st.ImportList(context.Background(), []store.Anime{toAnime(m)}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	a, _, _, err := st.AnimeRecord(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	got := FromRecord(a)

	if *got.Title.Romaji != *m.Title.Romaji || *got.Title.English != *m.Title.English ||
		*got.Status != "RELEASING" || *got.Episodes != 28 || *got.IDMal != 52991 {
		t.Fatalf("basics lost: %+v", got)
	}
	if len(got.Genres) != 2 || got.Genres[1] != "Drama" {
		t.Errorf("genres = %v", got.Genres)
	}
	if len(got.Studios.Nodes) != 1 || got.Studios.Nodes[0].Name != "Madhouse" {
		t.Errorf("studios = %+v", got.Studios)
	}
	if got.StartDate.Year == nil || *got.StartDate.Year != 2023 || got.StartDate.Month != nil {
		t.Errorf("start date = %+v", got.StartDate)
	}
	if got.NextAiring == nil || got.NextAiring.Episode != 12 || got.NextAiring.TimeUntilAiring <= 0 {
		t.Errorf("next airing = %+v", got.NextAiring)
	}
}
