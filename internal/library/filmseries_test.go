package library

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"kuro/internal/db"
	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// The Garden of Sinners as AniList has it: ten entries of one film each, chained by sequel
// edges, and a side story. Chapter 6 is the one a user could not play.
const (
	knkCh1, knkCh2, knkCh3, knkCh4, knkCh5 = 2593, 3782, 3783, 4280, 4282
	knkCh6, knkCh7, knkRemix, knkCh8       = 5204, 5205, 6624, 6954
	knkFuture, knkChorus                   = 14807, 20697
)

func openStore(t *testing.T) *store.Store {
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

func entryOf(id int, format, romaji, english, start string, episodes, minutes int) store.Anime {
	a := store.Anime{
		ID: id, Romaji: romaji, Format: &format, StartDate: start,
		Episodes: &episodes, Duration: &minutes, Synonyms: "[]", Genres: "[]",
	}
	if english != "" {
		a.English = &english
	}
	return a
}

func gardenStore(t *testing.T) *store.Store {
	t.Helper()
	st := openStore(t)
	ctx := context.Background()

	film := func(id int, romaji, english, start string, minutes int) store.Anime {
		return entryOf(id, "MOVIE", romaji, english, start, 1, minutes)
	}
	anime := []store.Anime{
		film(knkCh1, "Kara no Kyoukai: Fukan Fuukei", "the Garden of sinners Chapter 1: Thanatos. (Overlooking View)", "2007-12-01", 50),
		film(knkCh2, "Kara no Kyoukai: Satsujin Kousatsu (Zen)", "the Garden of sinners Chapter 2: …and nothing heart. (Murder Speculation Part A)", "2007-12-29", 58),
		film(knkCh3, "Kara no Kyoukai: Tsuukaku Zanryuu", "the Garden of sinners Chapter 3: ever cry, never life. (Remaining Sense of Pain)", "2008-01-26", 58),
		film(knkCh4, "Kara no Kyoukai: Garan no Dou", "the Garden of sinners Chapter 4: garan-no-dou. (The Hollow Shrine)", "2008-05-24", 46),
		film(knkCh5, "Kara no Kyoukai: Mujun Rasen", "the Garden of sinners Chapter 5: Paradox Paradigm", "2008-08-16", 114),
		film(knkCh6, "Kara no Kyoukai: Boukyaku Rokuon", "the Garden of sinners Chapter 6: Fairy Tale. (Oblivion Recording)", "2008-12-20", 59),
		film(knkCh7, "Kara no Kyoukai: Satsujin Kousatsu (Kou)", "the Garden of sinners Chapter 7: ……not nothing heart. (Murder Speculation Part B)", "2009-08-08", 119),
		film(knkRemix, "Kara no Kyoukai Remix: Gate of seventh heaven", "", "2009-03-14", 61),
		entryOf(knkCh8, "OVA", "Kara no Kyoukai: Shuushou", "the Garden of sinners Chapter 8: The Final Chapter", "2011-02-02", 1, 33),
		film(knkFuture, "Kara no Kyoukai: Mirai Fukuin", "the Garden of sinners -recalled out summer-", "2013-09-28", 90),
		film(knkChorus, "Kara no Kyoukai: Mirai Fukuin - extra chorus", "", "2013-09-28", 32),
	}
	if _, err := st.ImportList(ctx, anime, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	var rels []store.Relation
	chain := []int{knkCh1, knkCh2, knkCh3, knkCh4, knkCh5, knkCh6, knkCh7, knkRemix, knkCh8, knkFuture}
	for i := 1; i < len(chain); i++ {
		rels = append(rels, store.Relation{AnimeID: chain[i-1], RelatedID: chain[i], Kind: "SEQUEL"})
	}
	rels = append(rels, store.Relation{AnimeID: knkFuture, RelatedID: knkChorus, Kind: "SIDE_STORY"})
	if err := st.SaveRelations(ctx, rels); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

// askedIndexer answers every query with the same results and remembers what was asked.
type askedIndexer struct {
	results []indexer.Torrent
	mu      sync.Mutex
	asked   []string
}

func (*askedIndexer) Name() string { return "asked" }
func (a *askedIndexer) Search(_ context.Context, q indexer.Query) ([]indexer.Torrent, error) {
	a.mu.Lock()
	a.asked = append(a.asked, q.Text)
	a.mu.Unlock()
	return a.results, nil
}

func sized(hash, title string, seeders int, gigabytes float64) indexer.Torrent {
	t := release(hash, title, seeders)
	t.Size = int64(gigabytes * (1 << 30))
	return t
}

// What the user's two sites listed for "Kara no Kyoukai" on 2026-10-08.
func gardenReleases() []indexer.Torrent {
	return []indexer.Torrent{
		sized("jsu6", "[JacobSwaggedUp] Kara no Kyoukai 6: Boukyaku Rokuon | The Garden of Sinners Chapter 6: Oblivion Recording (BD 1280x720) [MP4 Movie]", 1, 0.9),
		sized("atime", "[Anime Time] Kara No Kyoukai (The Garden of Sinners) - Full Series [BD][All Movie][1080p][HEVC 10bit x265][AAC]", 83, 8.5),
		sized("ane", "[ANE] Kara no Kyoukai [BDRip 1080p x264 FLAC]", 40, 79.7),
		sized("neko1", "[nekotan] The Garden of Sinners S01E01 Chapter 1: Thanatos. (Overlooking View) (BD 1080p x265 10-bit Opus) | Kara no Kyoukai: Fukan Fuukei", 32, 3.4),
		sized("trix", "[Trix] Kara no Kyoukai (All Movies) [Eng subs - VOSTFR] (BD 1080p AV1) - The Garden of Sinners", 21, 4.9),
		sized("judas", "[Judas] Kara no Kyoukai (The Garden of Sinners) (Complete Movie Series) [BD 1080p][HEVC x265][Multi-Subs] (Movie)", 17, 12.0),
		sized("remux", "[UltraRemux] Kara no Kyoukai (The Garden of Sinners) - Complete Series + Manner Movies + OST + Extras (JPBD) (Lossless)", 14, 310),
		sized("utw9", "[UTW]_Kara_no_Kyoukai_-_Mirai_Fukuin_[BD][h264-1080p_FLAC][76F03FE0].mkv", 13, 10.9),
		sized("logn6", "[N LogN EG] Kara no Kyoukai (6) [H264 PS3 1080p AC3] [F43AEA3A].mp4", 0, 2.1),
		sized("pv", "[Darksoul-Subs] Kalafina - Sprinter -ufotable edit (Kara no Kyoukai) [640x480.Xvid][PV].avi", 3, 0.1),
	}
}

func findFilm(t *testing.T, st *store.Store, id int, results []indexer.Torrent) (Candidates, *askedIndexer) {
	t.Helper()
	idx := &askedIndexer{results: results}
	f := NewFinder(st, idx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got, err := f.Find(context.Background(), Request{AnimeID: id, Episode: 1, Prefs: score.DefaultPreferences()})
	if err != nil {
		t.Fatal(err)
	}
	return got, idx
}

// autoPicks are the releases playback would try, in order.
func autoPicks(c Candidates) []string {
	var out []string
	for _, r := range c.Results {
		if r.AutoPick {
			out = append(out, r.Torrent.InfoHash)
		}
	}
	return out
}

func byHash(t *testing.T, c Candidates, hash string) score.Result {
	t.Helper()
	for _, r := range c.Results {
		if r.Torrent.InfoHash == hash {
			return r
		}
	}
	t.Fatalf("release %q was dropped; kept: %d", hash, len(c.Results))
	return score.Result{}
}

func TestFilmOfASeriesSearchesTheSeriesName(t *testing.T) {
	_, idx := findFilm(t, gardenStore(t), knkCh6, nil)
	for _, want := range []string{"Kara no Kyoukai", "the Garden of sinners"} {
		if !slices.ContainsFunc(idx.asked, func(q string) bool { return strings.EqualFold(q, want) }) {
			t.Errorf("never searched %q; asked %q", want, idx.asked)
		}
	}
}

func TestFilmOfASeriesIsOfferedItsBatches(t *testing.T) {
	got, _ := findFilm(t, gardenStore(t), knkCh6, gardenReleases())

	// A well-seeded batch, not the film's own single release with one seeder. Which batch is the
	// usual ranking: here the one that plays without re-encoding.
	if got.Best == nil || !got.Best.Release.Batch || got.Best.Torrent.Seeders < 5 {
		t.Fatalf("best = %+v", got.Best)
	}
	order := autoPicks(got)
	for _, batch := range []string{"trix", "atime", "judas"} {
		if slices.Index(order, batch) > slices.Index(order, "jsu6") {
			t.Errorf("the one-seeder single release is tried before %s: %v", batch, order)
		}
	}
	for _, hash := range []string{"atime", "trix", "judas", "ane", "jsu6"} {
		if r := byHash(t, got, hash); !r.AutoPick {
			t.Errorf("%s is blocked: %s", hash, r.Blocked)
		}
	}
	// 80 GB over eleven films is 7 GB for one, inside the limit for an hour of film.
	if r := byHash(t, got, "ane"); r.EpisodeBytes() > 8<<30 || r.EpisodeBytes() < 6<<30 {
		t.Errorf("ANE's share for one film = %d bytes", r.EpisodeBytes())
	}

	for hash, want := range map[string]string{
		"neko1": "a different film of the series",
		"utw9":  "a different film of the series",
		"remux": "larger than the automatic size limit",
		"logn6": "no seeders",
		"pv":    "names a different show",
	} {
		if r := byHash(t, got, hash); r.AutoPick || r.Blocked != want {
			t.Errorf("%s: autoPick %v, blocked %q, want %q", hash, r.AutoPick, r.Blocked, want)
		}
	}

	// The single release names the film, so it is confirmed; so is a batch saying it holds them all.
	if !byHash(t, got, "jsu6").Confirmed || !byHash(t, got, "atime").Confirmed {
		t.Error("the film's own release or a whole-series batch is not confirmed")
	}
	if byHash(t, got, "ane").Confirmed {
		t.Error("a batch that does not say what it holds is confirmed before its files are read")
	}
}

func TestEveryFilmOfTheSeriesGetsItsOwnKey(t *testing.T) {
	st := gardenStore(t)
	f := NewFinder(st, fixedIndexer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for id, number := range map[int]int{knkCh1: 1, knkCh6: 6, knkCh7: 7, knkCh8: 8, knkFuture: 0, knkChorus: 0} {
		s := f.filmSeries(context.Background(), id)
		if s == nil {
			t.Errorf("%d is not seen as a film of the series", id)
			continue
		}
		if s.key.Number != number || s.key.Count != 11 {
			t.Errorf("%d: chapter %d of %d, want %d of 11", id, s.key.Number, s.key.Count, number)
		}
		if !slices.Contains(s.names, "Kara no Kyoukai") {
			t.Errorf("%d: series names %q lack the romaji one", id, s.names)
		}
	}
}

func TestFilmBatchIsPickedByTheFilmNotAnEpisodeNumber(t *testing.T) {
	st := gardenStore(t)
	f := NewFinder(st, fixedIndexer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	key := f.filmSeries(context.Background(), knkCh6).key

	judas := []torrent.File{
		{Name: "[Judas] Kara no Kyoukai - 01 - Fukan Fuukei.mkv", Length: 500 << 20},
		{Name: "[Judas] Kara no Kyoukai - 06 - Boukyaku Rokuon.mkv", Length: 650 << 20},
		{Name: "[Judas] Kara no Kyoukai - 07 - Boukyaku Rokuon.mkv", Length: 1200 << 20},
	}
	batch := score.Result{Candidate: score.Candidate{Film: &key}}
	batch.Release.Batch = true
	if file, i, ok := pickFile(judas, batch); !ok || i != 1 {
		t.Fatalf("picked %q (%d, %v), want file 06", file.Name, i, ok)
	}

	// The film's own release with unnamed extras beside it: the film is the big file.
	own := score.Result{Candidate: score.Candidate{Film: &key}}
	loose := []torrent.File{{Name: "extras/menu.mkv", Length: 30 << 20}, {Name: "movie.mkv", Length: 2 << 30}}
	if _, i, ok := pickFile(loose, own); !ok || i != 1 {
		t.Errorf("own release with extras: picked %d, %v", i, ok)
	}
	// The same unnamed files in a batch prove nothing.
	if _, _, ok := pickFile(loose, batch); ok {
		t.Error("picked an unnamed file out of a batch")
	}

	// An episode is still found by its number.
	pack := []torrent.File{{Name: "Show - 01.mkv", Length: 1 << 30}, {Name: "Show - 02.mkv", Length: 1 << 30}}
	if _, i, ok := pickFile(pack, score.Result{Candidate: score.Candidate{Numbers: []int{2}}}); !ok || i != 1 {
		t.Errorf("episode 2 of a pack: picked %d, %v", i, ok)
	}
}

// A film that belongs to a TV show: the show's packs hold episodes, so "Movie 1" must never be read as file 01.
func TestFilmAttachedToAShowIsSearchedAsBefore(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	anime := []store.Anime{
		entryOf(1, "TV", "Mahou Shoujo Example", "Magical Girl Example", "2015-01-01", 12, 24),
		entryOf(2, "MOVIE", "Mahou Shoujo Example: Movie 1", "Magical Girl Example: Movie 1", "2016-01-01", 1, 90),
		entryOf(3, "MOVIE", "Mahou Shoujo Example: Movie 2", "Magical Girl Example: Movie 2", "2017-01-01", 1, 90),
	}
	if _, err := st.ImportList(ctx, anime, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	rels := []store.Relation{
		{AnimeID: 1, RelatedID: 2, Kind: "SEQUEL"}, {AnimeID: 2, RelatedID: 3, Kind: "SEQUEL"},
	}
	if err := st.SaveRelations(ctx, rels); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}

	f := NewFinder(st, fixedIndexer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s := f.filmSeries(ctx, 2); s != nil {
		t.Fatalf("a film sharing its name with a TV show was treated as a film series: %+v", s.names)
	}
	// And an entry with no franchise at all.
	if s := f.filmSeries(ctx, 999); s != nil {
		t.Fatal("an unknown entry was treated as a film series")
	}

	pack := release("pack", "[Group] Mahou Shoujo Example (01-12) [BD 1080p] [Batch]", 50)
	got, _ := findFilm(t, st, 2, []indexer.Torrent{pack})
	for _, r := range got.Results {
		if r.Film != nil {
			t.Errorf("%q carries a film key", r.Torrent.Title)
		}
	}
}

// Groups number the later films differently, so a number with no title confirms nothing; and a
// patch file filed under the film's name is not the film.
func TestFilmNumberAloneIsNotConfirmation(t *testing.T) {
	releases := []indexer.Torrent{
		sized("named", "[Group] Kara no Kyoukai - Boukyaku Rokuon [BD 1080p]", 9, 2),
		sized("numbered", "[N LogN EG] Kara no Kyoukai (6) [H264 PS3 1080p AC3] [F43AEA3A].mp4", 9, 2),
		sized("patch", "(project-gxs)_Kara_no_Kyoukai_-_06_-_Oblivion_Recording_v2_(10bit_BD_1080p)_(ANE).gxs", 9, 0.1),
	}
	got, _ := findFilm(t, gardenStore(t), knkCh6, releases)

	if r := byHash(t, got, "named"); !r.Confirmed || !r.AutoPick {
		t.Errorf("the film by name: confirmed %v, blocked %q", r.Confirmed, r.Blocked)
	}
	if r := byHash(t, got, "numbered"); r.Confirmed || !r.AutoPick {
		t.Errorf("the film by number only: confirmed %v, blocked %q", r.Confirmed, r.Blocked)
	}
	if r := byHash(t, got, "patch"); r.AutoPick || r.Blocked != "not a video file" {
		t.Errorf("a patch file: autoPick %v, blocked %q", r.AutoPick, r.Blocked)
	}
	if got.Best == nil || got.Best.Torrent.InfoHash != "named" {
		t.Errorf("best = %+v", got.Best)
	}
}

// "Movies 1 - 8" numbers the films of the series, not episodes of this one.
func TestFilmBatchRangeIsNotAnEpisodeList(t *testing.T) {
	idx := &askedIndexer{results: append(gardenReleases(),
		sized("aruk", "[Arukoru] Kara No Kyoukai (The Garden of Sinners) Movies 1 - 8 & Remix [1080p x265 10bit BD Eng Sub AAC 5.1]", 98, 20))}
	f := NewFinder(gardenStore(t), idx, slog.New(slog.NewTextHandler(io.Discard, nil)))

	got, err := f.EpisodeNumbers(context.Background(), knkCh6)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a one-part film was given episodes %v", got)
	}
}

// The release list searches without playback's lookup of what a show is related to. Until the
// search does that itself, a film never opened before is not known to be one of a series.
func TestSearchLearnsTheSeriesBeforeLooking(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	anime := []store.Anime{
		entryOf(knkCh1, "MOVIE", "Kara no Kyoukai: Fukan Fuukei", "the Garden of sinners Chapter 1: Thanatos. (Overlooking View)", "2007-12-01", 1, 50),
		entryOf(knkCh6, "MOVIE", "Kara no Kyoukai: Boukyaku Rokuon", "the Garden of sinners Chapter 6: Fairy Tale. (Oblivion Recording)", "2008-12-20", 1, 59),
	}
	if _, err := st.ImportList(ctx, anime, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	idx := &askedIndexer{results: gardenReleases()}
	f := NewFinder(st, idx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	asked := 0
	f.WithFranchise(func(ctx context.Context, id int) error {
		asked++
		if err := st.SaveRelations(ctx, []store.Relation{{AnimeID: knkCh1, RelatedID: knkCh6, Kind: "SEQUEL"}}); err != nil {
			return err
		}
		_, err := st.RebuildFranchises(ctx)
		return err
	})

	got, err := f.Find(ctx, Request{AnimeID: knkCh6, Episode: 1, Prefs: score.DefaultPreferences()})
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatalf("relations looked up %d times, want once", asked)
	}
	if r := byHash(t, got, "neko1"); r.Blocked != "a different film of the series" {
		t.Errorf("chapter 1's release for chapter 6: blocked %q", r.Blocked)
	}
	if r := byHash(t, got, "atime"); !r.AutoPick || r.Film == nil {
		t.Errorf("the series batch is not offered: %+v", r.Blocked)
	}
}

func TestCommonPrefixNamesTheSeries(t *testing.T) {
	for want, titles := range map[string][]string{
		"Kara no Kyoukai": {"Kara no Kyoukai: Fukan Fuukei", "Kara no Kyoukai: Boukyaku Rokuon"},
		"the Garden of sinners": {
			"the Garden of sinners Chapter 1: Thanatos. (Overlooking View)", "the Garden of sinners Chapter 6: Fairy Tale.",
			"the Garden of sinners -recalled out summer-",
		},
		"": {"Only One Title"},
	} {
		if got := commonPrefix(titles); got != want {
			t.Errorf("commonPrefix(%q) = %q, want %q", titles, got, want)
		}
	}
	if got := commonPrefix([]string{"Alpha: One", "Beta: Two"}); got != "" {
		t.Errorf("unrelated titles share %q", got)
	}
}

func TestWholeSeriesSpotsABatchOfEveryFilm(t *testing.T) {
	for name, want := range map[string]bool{
		"[Anime Time] Kara No Kyoukai - Full Series [BD][All Movie][1080p]": true,
		"[Trix] Kara no Kyoukai (All Movies) (BD 1080p AV1)":                true,
		"[Judas] Kara no Kyoukai (Complete Movie Series) [BD 1080p]":        true,
		"[Arukoru] Kara No Kyoukai Movies 1 - 8 & Remix [1080p]":            true,
		"[JPSDR] Kara no Kyoukai [1-7][BDRip]":                              true,
		"[ANE] Kara no Kyoukai [BDRip 1080p x264 FLAC]":                     false,
		"[Coalgirls]_Kara_no_Kyoukai_(1920x1080_Blu-ray_FLAC)":              false,
	} {
		if got := wholeSeries.MatchString(name); got != want {
			t.Errorf("%q = %v, want %v", name, got, want)
		}
	}
}
