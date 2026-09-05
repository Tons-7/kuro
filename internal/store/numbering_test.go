package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"kuro/internal/db"
	"kuro/internal/metadata"
)

// Episodes used to be keyed by TheTVDB's count through its season, so a later
// cour started past one while everything else counted it from one. The
// migration re-keys such shows and everything filed under the old keys.
func TestMigrationRekeysALaterCourByItsOwnNumbering(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.MigrateTo(18); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.W.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	const bleach, plain = 185874, 1
	exec(`INSERT INTO anime (id, title_romaji, episode_count, status, synced_at)
	      VALUES (?, 'BLEACH: Sennen Kessen-hen - Kashin-tan', 10, 'RELEASING', 0)`, bleach)
	exec(`INSERT INTO anime (id, title_romaji, episode_count, synced_at) VALUES (?, 'Plain Show', 12, 0)`, plain)
	for n := 41; n <= 50; n++ {
		exec(`INSERT INTO episode (anime_id, ep_key, number, absolute) VALUES (?,?,?,?)`,
			bleach, strconv.Itoa(n), n, n+366)
	}
	for n := 1; n <= 12; n++ {
		exec(`INSERT INTO episode (anime_id, ep_key, number) VALUES (?,?,?)`, plain, strconv.Itoa(n), n)
	}

	// Episode 5 was played under both keys, the catalogue's more recently.
	exec(`INSERT INTO playback (anime_id, ep_key, position_s, last_played_at) VALUES (?, '45', 100, 200)`, bleach)
	exec(`INSERT INTO playback (anime_id, ep_key, position_s, last_played_at) VALUES (?, '5', 50, 100)`, bleach)
	exec(`INSERT INTO playback (anime_id, ep_key, position_s, last_played_at) VALUES (?, '44', 10, 100)`, bleach)
	exec(`INSERT INTO playback (anime_id, ep_key, position_s, last_played_at) VALUES (?, '5', 10, 100)`, plain)

	exec(`INSERT INTO torrent (info_hash, name, added_at) VALUES ('dkb', 'dkb', 0)`)
	exec(`INSERT INTO torrent_file (info_hash, file_index, path, size_bytes, anime_id, ep_key, selected)
	      VALUES ('dkb', 0, 'x.mkv', 1, ?, '45', 1)`, bleach)

	exec(`INSERT INTO download_queue (anime_id, ep_key, episode, queued_at) VALUES (?, '46', 46, 1)`, bleach)
	exec(`INSERT INTO download_queue (anime_id, ep_key, episode, queued_at) VALUES (?, '5', 5, 1)`, bleach)
	exec(`INSERT INTO download_queue (anime_id, ep_key, episode, queued_at) VALUES (?, '45', 45, 2)`, bleach)

	// Progress 44 of 10 completed a show still airing.
	exec(`INSERT INTO list_entry (id, anime_id, status, progress, completed_at) VALUES (-1, ?, 'COMPLETED', 44, '2026-08-19')`, bleach)
	exec(`INSERT INTO list_entry (id, anime_id, status, progress) VALUES (-2, ?, 'COMPLETED', 12)`, plain)
	exec(`INSERT INTO notification (kind, anime_id, episode, title, created_at) VALUES ('release', ?, 45, 't', 0)`, bleach)

	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	ctx := context.Background()

	eps, err := s.Episodes(ctx, bleach)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 10 {
		t.Fatalf("%d episodes, want 10", len(eps))
	}
	for i, e := range eps {
		if e.Number != i+1 || e.Display != i+1 || e.EpKey != strconv.Itoa(i+1) {
			t.Errorf("episode %d: number %d display %d key %q", i, e.Number, e.Display, e.EpKey)
		}
	}
	if alias, _ := s.EpisodeAlias(ctx, bleach, 5); alias.Tvdb != 45 || alias.Absolute != 411 {
		t.Errorf("alias = %+v, want TVDB 45, absolute 411", alias)
	}

	played, err := s.playbackByEpisode(ctx, bleach)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := played["5"]; !ok || p.position != 100 {
		t.Errorf("episode 5 playback = %+v (%v), want the later one at 100s", p, ok)
	}
	if _, ok := played["4"]; !ok {
		t.Error("episode 44 was not re-keyed to 4")
	}
	if _, ok := played["45"]; ok {
		t.Error("the old key 45 survived")
	}

	var key string
	if err := conn.R.QueryRow(`SELECT ep_key FROM torrent_file WHERE info_hash = 'dkb'`).Scan(&key); err != nil || key != "5" {
		t.Errorf("torrent file key = %q (%v), want 5", key, err)
	}

	queued, err := s.QueuedDownloads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]int{}
	for _, q := range queued {
		keys[q.EpKey] = q.Episode
	}
	if len(keys) != 2 || keys["5"] != 5 || keys["6"] != 6 {
		t.Errorf("queue = %v, want episodes 5 and 6 once each", keys)
	}

	var status string
	var progress, dirty int
	if err := conn.R.QueryRow(`SELECT status, progress, dirty FROM list_entry WHERE anime_id = ?`, bleach).
		Scan(&status, &progress, &dirty); err != nil {
		t.Fatal(err)
	}
	if status != "CURRENT" || progress != 4 || dirty != 1 {
		t.Errorf("list entry = %s %d dirty=%d, want CURRENT 4 dirty", status, progress, dirty)
	}
	var episode int
	if err := conn.R.QueryRow(`SELECT episode FROM notification WHERE anime_id = ?`, bleach).Scan(&episode); err != nil || episode != 5 {
		t.Errorf("notification episode = %d (%v), want 5", episode, err)
	}

	// A show counted from one everywhere is left alone.
	plainEps, _ := s.Episodes(ctx, plain)
	if len(plainEps) != 12 || plainEps[4].EpKey != "5" {
		t.Errorf("plain show changed: %d episodes", len(plainEps))
	}
	if err := conn.R.QueryRow(`SELECT status, progress FROM list_entry WHERE anime_id = ?`, plain).
		Scan(&status, &progress); err != nil || status != "COMPLETED" || progress != 12 {
		t.Errorf("plain list entry = %s %d, want COMPLETED 12", status, progress)
	}
}

// The catalogue keeps the entry's numbering and TVDB's beside it; a refresh
// that no longer lists a numbered row drops it, since it was keyed the old way.
func TestSaveEpisodesKeepsAliasesAndPrunesStaleRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EnsureAnime(ctx, 7); err != nil {
		t.Fatal(err)
	}

	save := func(eps ...metadata.Episode) {
		t.Helper()
		if _, err := s.SaveEpisodes(ctx, 7, eps); err != nil {
			t.Fatal(err)
		}
	}
	save(
		metadata.Episode{Number: 1, TvdbNumber: 41, Absolute: 407},
		metadata.Episode{Number: 2, TvdbNumber: 42},
		metadata.Episode{Number: 3, TvdbNumber: 43},
	)
	save(
		metadata.Episode{Number: 1, TvdbNumber: 41, Absolute: 407},
		metadata.Episode{Number: 2, TvdbNumber: 42},
	)

	eps, err := s.Episodes(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 || eps[1].Display != 2 {
		t.Fatalf("episodes = %+v, want 1 and 2 displayed as such", eps)
	}

	if alias, _ := s.EpisodeAlias(ctx, 7, 1); alias.Tvdb != 41 || alias.Absolute != 407 {
		t.Errorf("alias = %+v", alias)
	}
	if got := (EpisodeAlias{Tvdb: 42}).Numbers(); len(got) != 1 || got[0] != 42 {
		t.Errorf("numbers = %v", got)
	}

	// Numbered the same everywhere: nothing to alias.
	if err := s.EnsureAnime(ctx, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveEpisodes(ctx, 8, []metadata.Episode{{Number: 1, TvdbNumber: 1, Absolute: 1}}); err != nil {
		t.Fatal(err)
	}
	if alias, _ := s.EpisodeAlias(ctx, 8, 1); len(alias.Numbers()) != 0 {
		t.Errorf("alias = %+v, want none", alias)
	}
	if alias, _ := s.EpisodeAlias(ctx, 9, 1); len(alias.Numbers()) != 0 {
		t.Errorf("unknown show has alias %+v", alias)
	}
}
