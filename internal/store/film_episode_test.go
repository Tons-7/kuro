package store

import (
	"context"
	"testing"
)

func film(t *testing.T, s *Store, id int, format string, episodes int, description, banner, cover any) {
	t.Helper()
	if _, err := s.w.Exec(`
		INSERT INTO anime (id, title_romaji, format, episode_count, description, banner_url, cover_url, duration, synced_at)
		VALUES (?, 'Film', ?, ?, ?, ?, ?, 59, 0)`, id, format, episodes, description, banner, cover); err != nil {
		t.Fatal(err)
	}
}

// No source keeps episode data for a film, so its one episode is described by the film's own entry.
func TestAFilmIsItsOwnEpisode(t *testing.T) {
	s := newTestStore(t)
	film(t, s, 1, "MOVIE", 1, "A girl <i>forgets</i>.<br><br>\n(Source: Somewhere) &amp; more", "banner.jpg", "cover.jpg")

	eps, err := s.Episodes(context.Background(), 1)
	if err != nil || len(eps) != 1 {
		t.Fatalf("episodes = %+v, err %v", eps, err)
	}
	e := eps[0]
	if e.Planned {
		t.Error("a film's only part is still marked as a guess")
	}
	if e.TitleEN == nil || *e.TitleEN != "Complete Movie" {
		t.Errorf("title = %v", e.TitleEN)
	}
	if e.Overview == nil || *e.Overview != "A girl forgets. (Source: Somewhere) & more" {
		t.Errorf("overview = %v", e.Overview)
	}
	if e.Still == nil || *e.Still != "banner.jpg" {
		t.Errorf("still = %v", e.Still)
	}
	if e.Runtime == nil || *e.Runtime != 59 {
		t.Errorf("runtime = %v", e.Runtime)
	}
}

// The cover stands in when there is no banner, and a one-part OVA is described the same way but keeps its number.
func TestAOnePartOVAIsDescribedByItsEntry(t *testing.T) {
	s := newTestStore(t)
	film(t, s, 1, "OVA", 1, "An extra.", nil, "cover.jpg")

	eps, _ := s.Episodes(context.Background(), 1)
	if len(eps) != 1 || eps[0].TitleEN != nil || eps[0].Planned {
		t.Fatalf("episodes = %+v", eps)
	}
	if eps[0].Overview == nil || *eps[0].Overview != "An extra." || eps[0].Still == nil || *eps[0].Still != "cover.jpg" {
		t.Errorf("overview %v, still %v", eps[0].Overview, eps[0].Still)
	}
}

// What a source did record wins; only the gaps are filled.
func TestAFilmKeepsWhatASourceRecorded(t *testing.T) {
	s := newTestStore(t)
	film(t, s, 1, "MOVIE", 1, "From the entry.", "banner.jpg", "cover.jpg")
	if _, err := s.w.Exec(`
		INSERT INTO episode (anime_id, ep_key, number, title_en, overview, still_url)
		VALUES (1, '1', 1, 'The Film', 'From the source.', NULL)`); err != nil {
		t.Fatal(err)
	}

	eps, _ := s.Episodes(context.Background(), 1)
	if len(eps) != 1 || *eps[0].TitleEN != "The Film" || *eps[0].Overview != "From the source." {
		t.Fatalf("episodes = %+v", eps)
	}
	if eps[0].Still == nil || *eps[0].Still != "banner.jpg" {
		t.Errorf("still = %v, want the gap filled", eps[0].Still)
	}
}

// A film not out yet is described too, but still marked as expected rather than as there to watch.
func TestAnUnreleasedFilmStaysExpected(t *testing.T) {
	s := newTestStore(t)
	film(t, s, 1, "MOVIE", 1, "Coming.", nil, "cover.jpg")
	if _, err := s.w.Exec(`UPDATE anime SET status = 'NOT_YET_RELEASED' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	eps, _ := s.Episodes(context.Background(), 1)
	if len(eps) != 1 || !eps[0].Planned || eps[0].Overview == nil {
		t.Fatalf("episodes = %+v", eps)
	}
}

// A show's episodes are never described by the show: only a one-part entry is its own episode.
func TestAShowIsNotItsOwnEpisode(t *testing.T) {
	s := newTestStore(t)
	film(t, s, 1, "TV", 3, "The whole show.", "banner.jpg", "cover.jpg")

	eps, _ := s.Episodes(context.Background(), 1)
	if len(eps) != 3 {
		t.Fatalf("got %d episodes", len(eps))
	}
	for _, e := range eps {
		if e.Overview != nil || e.Still != nil || !e.Planned {
			t.Errorf("episode %d = %+v", e.Number, e)
		}
	}
}
