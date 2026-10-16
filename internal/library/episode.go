package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode"
	"unicode/utf8"

	"kuro/internal/film"
	"kuro/internal/indexer"
	"kuro/internal/parse"
	"kuro/internal/score"
	"kuro/internal/store"
)

// Finder locates releases for an anime already chosen, so parsing verifies a
// result rather than identifying it: a wrong season is rejected outright.
type Finder struct {
	store   *store.Store
	indexer indexer.Source
	// adult is searched only for titles the catalogue marks as such: the main
	// trackers carry none, and searching it always would leak into results.
	adult indexer.Source
	log   *slog.Logger
	// Whether this machine transcodes in real time; atomic, as ffmpeg can arrive mid-session.
	hwTranscode atomic.Bool
	// hydrate fetches a show the catalogue lacks (a fresh install has none yet); nil skips it.
	hydrate func(ctx context.Context, ids []int) (int, error)
	// franchise learns the show's relations: cours and films are told apart by their siblings. Nil skips it.
	franchise func(ctx context.Context, animeID int) error
}

// WithFranchise has every search learn the show's relations first, not only the ones playback starts.
func (f *Finder) WithFranchise(ensure func(ctx context.Context, animeID int) error) *Finder {
	f.franchise = ensure
	return f
}

// WithHydrator lets Find fetch a show it has no titles for, instead of failing.
func (f *Finder) WithHydrator(h func(ctx context.Context, ids []int) (int, error)) *Finder {
	f.hydrate = h
	return f
}

// NewFinder searches idx, which is nil when no site is configured.
func NewFinder(s *store.Store, idx indexer.Source, log *slog.Logger) *Finder {
	return &Finder{store: s, indexer: idx, log: log}
}

// ErrNoIndexers is the answer to any search while no site is configured.
var ErrNoIndexers = errors.New(
	"no release sources configured: add [[indexer]] blocks to config.toml and restart")

// WithHardwareTranscode tells the finder whether playback can hardware-transcode,
// which decides whether HEVC/Hi10P releases are ranked down as stall-prone.
func (f *Finder) WithHardwareTranscode(ok bool) *Finder {
	f.hwTranscode.Store(ok)
	return f
}

func (f *Finder) WithAdultIndexer(idx indexer.Source) *Finder {
	f.adult = idx
	return f
}

type Request struct {
	AnimeID int
	Episode int
	Season  int

	// Alias is the same episode under the numberings a release may use instead:
	// counted through TheTVDB's long season, or absolute over the whole show.
	Alias store.EpisodeAlias

	// Part is this entry's cour, from its title; other cours reuse the same episode numbers.
	Part int

	// Numerals are the numbers this entry's own titles end in ("Kaiju No. 8"): part of the name, not a season.
	Numerals []int

	// Counted is the franchise's season for an entry whose titles state none; a release saying so is not wrong.
	Counted int

	Cour Cour

	Prefs score.Preferences
}

// Cour tells a split-cour entry from its siblings, which restart numbering under one base title.
type Cour struct {
	// Own are words only this entry's titles carry ("kashin", "calamity");
	// Siblings are words only one other cour's carry ("soukoku", "conflict").
	Own      []string
	Siblings []string

	// Later marks an entry that is not the first cour. A release naming no
	// cour counts from the first, so its bare number is some other episode.
	Later bool

	// Bases: the titles' words minus the cour subtitle, as releases counting across cours name it.
	Bases [][]string
}

type Candidates struct {
	Queries []string       `json:"queries"`
	Results []score.Result `json:"results"`
	Best    *score.Result  `json:"best,omitempty"`
}

// learnRelations is bounded: a throttled AniList must not hold up every search.
func (f *Finder) learnRelations(ctx context.Context, animeID int) {
	if f.franchise == nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, relationsDeadline)
	defer cancel()
	if err := f.franchise(bounded, animeID); err != nil {
		f.log.Warn("franchise for matching", "anime", animeID, "err", err)
	}
}

// Find searches the show's titles, keeping releases that verify as the requested episode and season.
func (f *Finder) Find(ctx context.Context, req Request) (Candidates, error) {
	f.learnRelations(ctx, req.AnimeID)

	titles, err := f.store.SearchTitles(ctx, req.AnimeID)
	if err != nil {
		return Candidates{}, err
	}
	if len(titles) == 0 && f.hydrate != nil {
		if _, err := f.hydrate(ctx, []int{req.AnimeID}); err != nil {
			f.log.Warn("fetch unknown show", "anime", req.AnimeID, "err", err)
		}
		if titles, err = f.store.SearchTitles(ctx, req.AnimeID); err != nil {
			return Candidates{}, err
		}
	}
	total, _ := f.store.EpisodeCount(ctx, req.AnimeID)
	runtime := f.store.EpisodeRuntime(ctx, req.AnimeID)
	bestByHash, bestByGroup, _ := f.store.SeaDexFor(ctx, req.AnimeID)

	// The curated release is rarely the most seeded, so it is asked for by name.
	groups, _ := f.store.BestGroups(ctx, req.AnimeID)
	english, _ := f.store.EnglishTitle(ctx, req.AnimeID)
	req = f.numbering(ctx, req, titles, english)
	req.Prefs.HardwareTranscode = f.hwTranscode.Load()

	// One film of a film series: its well-kept copies are batches named after the series.
	var series *filmSeries
	if req.Episode <= 1 {
		series = f.filmSeries(ctx, req.AnimeID)
	}
	queries := searchTerms(titles, english, req.Episode, req.Alias.Tvdb, groups, req.Prefs.Audio == "dub", series.queries()...)

	// Several seconds per request, and the variants are independent: run
	// sequentially they put half a minute between pressing play and anything.
	source := f.indexer
	adult, adultErr := f.store.IsAdult(ctx, req.AnimeID)
	if adult && f.adult != nil {
		source = f.adult
		f.log.Info("searching the adult tracker", "anime", req.AnimeID)
	} else if adultErr != nil {
		f.log.Warn("adult flag unreadable", "anime", req.AnimeID, "err", adultErr)
	}
	if source == nil {
		return Candidates{}, ErrNoIndexers
	}
	batches := f.searchAll(ctx, source, queries)

	// Every site failing is an outage, not "nobody released it".
	if err := allFailed(batches); err != nil {
		return Candidates{}, err
	}

	// Trackers match every token, so one romanisation spelled differently
	// ("Semi" for "Zemi") matches nothing. Only paid for when nothing was found.
	if countResults(batches) == 0 {
		if short := shortTerms(titles, english); len(short) > 0 {
			batches = append(batches, f.searchAll(ctx, source, short)...)
		}
	}

	identity := newShowIdentity(append(append([]string{}, titles...), english))

	seen := map[string]struct{}{}
	var cands []score.Candidate
	var used []string

	for _, batch := range batches {
		results := batch.results
		used = append(used, batch.query)
		if batch.err != nil {
			f.log.Warn("indexer search failed", "query", batch.query, "err", batch.err)
			continue
		}

		for _, t := range results {
			if _, dup := seen[t.InfoHash]; dup {
				continue
			}
			seen[t.InfoHash] = struct{}{}

			rel := parse.Parse(t.Title)
			c := score.Candidate{
				Torrent: t, Release: rel, TotalEpisodes: total, RuntimeMinutes: runtime,
				// Kept, not dropped: the picker shows what was found and why not.
				WrongShow: !identity.matches(rel, t.Title),
			}
			if series != nil {
				// Every film is "episode 1", so the number verifies nothing; the name does.
				c.Film = &series.key
				switch series.key.Match(t.Title) {
				case film.Ours:
					c.Confirmed = series.key.Named(t.Title)
				case film.Other:
					c.WrongFilm = true
				default:
					// The series by name only: a batch, whose file list says whether the film is in it.
					c.Release.Batch = true
					c.Confirmed = wholeSeries.MatchString(t.Title)
				}
			} else {
				if !verifies(rel, req) {
					continue
				}
				c.Confirmed, c.Numbers, c.Episode = confirms(rel, req), numbersFor(rel, req), req.Episode
			}
			if info, ok := bestByHash[strings.ToLower(t.InfoHash)]; ok {
				c.SeaDexBest, c.SeaDexGroup = info.IsBest, true
			} else if _, ok := bestByGroup[strings.ToLower(rel.Group)]; ok {
				// Private trackers redact the hash; the group is all that survives.
				c.SeaDexGroup = true
			}
			cands = append(cands, c)
		}
	}

	out := Candidates{Queries: used, Results: score.Rank(cands, req.Prefs)}
	if len(out.Results) > 0 && out.Results[0].AutoPick {
		best := out.Results[0]
		out.Best = &best
	}
	return out, nil
}

func allFailed(batches []searchBatch) error {
	var errs []error
	for _, b := range batches {
		if b.err == nil {
			return nil
		}
		errs = append(errs, b.err)
	}
	if len(errs) == 0 {
		return nil
	}
	return searchFailed{errs}
}

// searchFailed reads as one line per site, the raw errors attached for the log and errors.Is.
type searchFailed struct{ errs []error }

func (e searchFailed) Unwrap() []error { return e.errs }

func (e searchFailed) Error() string {
	var parts []string
	for _, err := range flatten(e.errs) {
		if p := siteProblem(err); !slices.Contains(parts, p) {
			parts = append(parts, p)
		}
	}
	return "couldn't search for releases: " + strings.Join(parts, "; ")
}

func flatten(errs []error) []error {
	var out []error
	for _, err := range errs {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			out = append(out, flatten(joined.Unwrap())...)
		} else {
			out = append(out, err)
		}
	}
	return out
}

// siteProblem names the site and the cause in a few words; indexer errors start "site: ".
func siteProblem(err error) string {
	site, rest, ok := strings.Cut(err.Error(), ": ")
	if !ok {
		return err.Error()
	}
	var timeout interface{ Timeout() bool }
	var dns *net.DNSError
	switch {
	case errors.As(err, &timeout) && timeout.Timeout():
		return site + " timed out"
	case errors.As(err, &dns):
		return site + " could not be found"
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(rest, "refused"):
		return site + " refused the connection"
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return site + " is unreachable"
	}
	if len(rest) > 80 {
		rest = rest[:80] + "…"
	}
	return site + ": " + rest
}

func countResults(batches []searchBatch) int {
	var n int
	for _, b := range batches {
		n += len(b.results)
	}
	return n
}

// EpisodeNumbers reports which episodes have releases, the only evidence for an uncatalogued show.
func (f *Finder) EpisodeNumbers(ctx context.Context, animeID int) ([]int, error) {
	// One film of a series is one part; the numbers in its releases count films, as "Movies 1 - 8" does.
	f.learnRelations(ctx, animeID)
	if f.filmSeries(ctx, animeID) != nil {
		return nil, nil
	}
	found, err := f.Find(ctx, Request{AnimeID: animeID})
	if err != nil {
		return nil, err
	}

	seen := map[int]struct{}{}
	for _, r := range found.Results {
		// Kept for the picker, but its numbers are another show's, and a derived row is never removed.
		if r.WrongShow {
			continue
		}
		rel := r.Release
		switch {
		// A batch states the range it holds, which is the whole list at once.
		case rel.EpisodeEnd > rel.Episode && rel.Episode > 0:
			for n := rel.Episode; n <= rel.EpisodeEnd && n-rel.Episode < 2000; n++ {
				seen[n] = struct{}{}
			}
		case rel.Episode > 0:
			seen[rel.Episode] = struct{}{}
		}
	}

	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out, nil
}

// shortTerms are last-resort queries: the title's most distinctive words. A
// proper noun survives romanisation differences a full title does not.
func shortTerms(titles []string, english string) []string {
	var out []string
	seen := map[string]struct{}{}

	// Latin-only and cleaned like any query: only three are kept, so a native-script
	// word would crowd out the fallbacks this exists to provide.
	add := func(s string) {
		s = cleanQuery(s)
		if utf8.RuneCountInString(s) < 4 || len(out) >= 3 || !mostlyLatin(s) {
			return
		}
		key := strings.ToLower(s)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}

	for _, title := range append(append([]string{}, titles...), english) {
		words := significantWords(title)
		if len(words) == 0 {
			continue
		}
		// The longest word is usually the name the show is known by.
		longest := words[0]
		for _, w := range words {
			if len(w) > len(longest) {
				longest = w
			}
		}
		add(longest)
		if len(words) >= 2 {
			add(words[0] + " " + words[1])
		}
	}
	return out
}

var wordSplit = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// Particles and articles match everything and narrow nothing.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "and": true, "to": true,
	"no": true, "ga": true, "wa": true, "ni": true, "de": true, "wo": true,
	"season": true, "part": true, "movie": true, "tv": true,
}

func significantWords(title string) []string {
	var out []string
	for _, w := range wordSplit.Split(title, -1) {
		// In characters, not bytes: a two-character Japanese word is six bytes.
		if utf8.RuneCountInString(w) < 4 || stopWords[strings.ToLower(w)] {
			continue
		}
		if _, err := strconv.Atoi(w); err == nil {
			continue
		}
		out = append(out, w)
	}
	return out
}

// numbering fills in the cour and alternate number: catalogues count on, groups restart at one.
func (f *Finder) numbering(ctx context.Context, req Request, titles []string, english string) Request {
	for _, t := range append([]string{english}, titles...) {
		if n := parse.PartOf(t); n > 0 {
			req.Part = n
			break
		}
	}
	for _, t := range append([]string{english}, titles...) {
		// "Kaiju No. 8 Season 2" ends in 2, its name in 8.
		for _, name := range []string{t, baseTitle(t)} {
			if n, _ := parse.NumeralSeason(name); n > 0 && !slices.Contains(req.Numerals, n) {
				req.Numerals = append(req.Numerals, n)
			}
		}
	}

	// A caller that named no season gets the one the title states, or the first.
	// Otherwise episode 1 would accept an "S02E01" release, a different episode.
	if req.Season == 0 {
		req.Season = f.defaultSeason(ctx, req.AnimeID, append([]string{english}, titles...))
		// "Dr. Stone: New World" states nothing, but it is the third series of its franchise.
		if req.Season == 1 {
			req.Counted = f.franchiseSeason(ctx, req.AnimeID)
		}
	}

	req.Alias, _ = f.store.EpisodeAlias(ctx, req.AnimeID, req.Episode)
	if !req.Alias.StatedTvdb || !req.Alias.StatedAbsolute {
		derived := f.derivedAlias(ctx, req, titles, english)
		if !req.Alias.StatedTvdb {
			req.Alias.Tvdb = derived.Tvdb
		}
		if !req.Alias.StatedAbsolute {
			req.Alias.Absolute = derived.Absolute
		}
	}
	req.Cour = f.cour(ctx, req, titles, english)
	return req
}

// defaultSeason is the title's season, else a sequel numeral: a digit only past the first entry (Kaiju No. 8).
func (f *Finder) defaultSeason(ctx context.Context, animeID int, titles []string) int {
	for _, t := range titles {
		if n := parse.SeasonOf(t); n > 0 {
			return n
		}
	}
	for _, t := range titles {
		n, roman := parse.NumeralSeason(t)
		if n == 0 {
			continue
		}
		if roman || f.laterSeason(ctx, animeID) {
			return n
		}
	}
	return 1
}

// franchiseSeason counts the series up to this entry (a later cour part continues its season); 0 if unplaced.
func (f *Finder) franchiseSeason(ctx context.Context, animeID int) int {
	franchise, err := f.store.Franchise(ctx, animeID)
	if err != nil {
		return 0
	}
	season := 0
	for _, s := range franchise.Seasons {
		if !series(s.Format) {
			if s.ID == animeID {
				return 0
			}
			continue
		}
		english := ""
		if s.English != nil {
			english = *s.English
		}
		if season == 0 || max(parse.PartOf(s.Romaji), parse.PartOf(english)) < 2 {
			season++
		}
		if s.ID == animeID {
			return season
		}
	}
	return 0
}

func (f *Finder) laterSeason(ctx context.Context, animeID int) bool {
	franchise, err := f.store.Franchise(ctx, animeID)
	if err != nil {
		return false
	}
	for _, s := range franchise.Seasons {
		if s.ID == animeID {
			return s.Ordinal > 1
		}
	}
	return false
}

// derivedAlias counts through the franchise: earlier cours give the season's number, all entries the absolute.
func (f *Finder) derivedAlias(ctx context.Context, req Request, titles []string, english string) store.EpisodeAlias {
	if req.Episode <= 0 {
		return store.EpisodeAlias{}
	}
	franchise, err := f.store.Franchise(ctx, req.AnimeID)
	if err != nil || len(franchise.Seasons) < 2 {
		return store.EpisodeAlias{}
	}

	bases := map[string]bool{}
	for _, t := range append([]string{english}, titles...) {
		if b := baseTitle(t); b != "" {
			bases[cleanQuery(b)] = true
		}
	}

	var absolute, run int
	var counted, split bool
	for _, s := range franchise.Seasons {
		if s.ID == req.AnimeID {
			break
		}
		if !series(s.Format) {
			continue
		}
		if s.Episodes == nil || *s.Episodes <= 0 {
			return store.EpisodeAlias{}
		}
		absolute += *s.Episodes

		// An earlier series under another name restarts the count.
		names, _ := f.store.SearchTitles(ctx, s.ID)
		run += *s.Episodes
		if namedBy(bases, append(names, s.Romaji)) {
			counted = true
			continue
		}
		// A run broken by another entry restarts somewhere unknown; a guessed number is some other episode.
		split = split || counted
		run = 0
	}

	alias := store.EpisodeAlias{}
	if run > 0 && !split {
		alias.Tvdb = run + req.Episode
	}
	if absolute > 0 {
		alias.Absolute = absolute + req.Episode
	}
	return alias
}

// namedBy reports that one of the names is this entry's title without its cour.
func namedBy(bases map[string]bool, names []string) bool {
	for _, n := range names {
		if n == "" {
			continue
		}
		if bases[cleanQuery(n)] {
			return true
		}
		if b := baseTitle(n); b != "" && bases[cleanQuery(b)] {
			return true
		}
	}
	return false
}

// cour works out, from the franchise, what separates this entry's releases from a sibling cour's.
func (f *Finder) cour(ctx context.Context, req Request, titles []string, english string) Cour {
	all := append([]string{english}, titles...)
	mine := wordSet(all...)
	own := wordSet(all...)
	c := Cour{
		// A cour TVDB counts past one follows another under the same season.
		Later: req.Season > 1 || req.Part > 1 || req.Alias.Tvdb > req.Episode,
	}
	for _, t := range all {
		base := baseTitle(t)
		if base == "" {
			base = t
		}
		if words := significantWords(base); len(words) > 0 {
			c.Bases = append(c.Bases, lower(words))
		}
	}

	franchise, err := f.store.Franchise(ctx, req.AnimeID)
	if err != nil {
		return c
	}
	ordinal := 0
	for _, s := range franchise.Seasons {
		if s.ID == req.AnimeID {
			ordinal = s.Ordinal
		}
	}

	// A word in one sibling only names that cour; one several share ("TYBW") names the show.
	count := map[string]int{}
	for _, s := range franchise.Seasons {
		if s.ID == req.AnimeID {
			continue
		}
		names, _ := f.store.SearchTitles(ctx, s.ID)
		theirs := wordSet(names...)
		for w := range theirs {
			if !mine[w] {
				count[w]++
			}
		}
		// An earlier series named by this entry's base title is the cour the bare name belongs to.
		if s.Ordinal < ordinal && series(s.Format) {
			for _, name := range names {
				for _, t := range all {
					if b := baseTitle(t); b != "" && strings.EqualFold(cleanQuery(b), cleanQuery(name)) {
						c.Later = true
					}
				}
			}
		}
		for w := range theirs {
			delete(own, w)
		}
	}
	for w, n := range count {
		if n == 1 {
			c.Siblings = append(c.Siblings, w)
		}
	}
	for w := range own {
		c.Own = append(c.Own, w)
	}
	slices.Sort(c.Siblings)
	slices.Sort(c.Own)
	return c
}

func series(format *string) bool {
	if format == nil {
		return true
	}
	switch strings.ToUpper(*format) {
	case "MOVIE", "SPECIAL", "OVA", "MUSIC":
		return false
	}
	return true
}

func wordSet(titles ...string) map[string]bool {
	out := map[string]bool{}
	for _, t := range titles {
		for _, w := range significantWords(t) {
			out[strings.ToLower(w)] = true
		}
	}
	return out
}

func lower(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = strings.ToLower(w)
	}
	return out
}

type searchBatch struct {
	query   string
	results []indexer.Torrent
	err     error
}

// Enough to collapse the wait, few enough not to burst a public tracker.
const searchConcurrency = 4

// searchAll runs the variants together, keeping request order, which ranks the likeliest title.
func (f *Finder) searchAll(ctx context.Context, source indexer.Source, queries []string) []searchBatch {
	out := make([]searchBatch, len(queries))
	slots := make(chan struct{}, searchConcurrency)

	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()

			// No category: each source knows its own (nyaa's numbers differ on sukebei).
			results, err := source.Search(ctx, indexer.Query{Text: q})
			out[i] = searchBatch{query: q, results: results, err: err}
		})
	}
	wg.Wait()
	return out
}

// marked reports whether the release names this cour, or a sibling cour, by a
// word only that cour's titles carry or by an agreeing season or part marker.
func marked(rel parse.Release, req Request) (own, sibling bool) {
	words := wordSet(rel.Title)
	for _, w := range req.Cour.Siblings {
		sibling = sibling || words[w]
	}
	for _, w := range req.Cour.Own {
		own = own || words[w]
	}
	own = own || (req.Part > 0 && rel.Part == req.Part) ||
		(req.Season > 1 && (rel.Season == req.Season || numeralSeason(rel, req) == req.Season))
	return own, sibling
}

// numeralSeason: "Overlord IV - 05" is season 4; a digit the show's own name ends in is not a season.
func numeralSeason(rel parse.Release, req Request) int {
	if rel.Season > 0 || rel.Part > 0 {
		return 0 // "Part 2" is the numeral it ends in
	}
	n, roman := parse.NumeralSeason(rel.Title)
	if n < 2 || (!roman && slices.Contains(req.Numerals, n) && req.Season != n) {
		return 0
	}
	return n
}

// bare: a later cour's release naming no cour counts from the first, so its number isn't this entry's.
func bare(rel parse.Release, req Request) bool {
	own, _ := marked(rel, req)
	return req.Cour.Later && !own
}

// numbersFor lists the numbers a file for the episode may carry in this
// release, the one asked for first so the pack picker knows which is primary.
func numbersFor(rel parse.Release, req Request) []int {
	var out []int
	if !bare(rel, req) {
		out = append(out, req.Episode)
	}
	return append(out, req.Alias.Numbers()...)
}

// covers reports that a batch's stated range holds the number; a batch stating no range may hold anything.
func covers(rel parse.Release, n int) bool {
	return rel.Batch && rel.Episode <= n && (rel.EpisodeEnd == 0 || n <= rel.EpisodeEnd)
}

// namesShow: the release names this entry without its cour ("Bleach - 45" is the original series').
func namesShow(rel parse.Release, req Request) bool {
	words := wordSet(rel.Title)
	for _, base := range req.Cour.Bases {
		var hits int
		for _, w := range base {
			if words[w] {
				hits++
			}
		}
		if float64(hits)/float64(len(base)) >= 0.5 {
			return true
		}
	}
	return len(req.Cour.Bases) == 0
}

// names: the release names enough of a base title, or shortens one ("Bleach" for Bleach TYBW).
func names(rel parse.Release, req Request) bool {
	if namesShow(rel, req) {
		return true
	}
	words := wordSet(rel.Title)
	if len(words) == 0 {
		return false
	}
	for _, base := range req.Cour.Bases {
		all := true
		for w := range words {
			all = all && slices.Contains(base, w)
		}
		if all {
			return true
		}
	}
	return false
}

// broadcast reports a later cour's release stating the season TheTVDB files it under, in this show's name.
func broadcast(rel parse.Release, req Request) bool {
	return rel.Season > 1 && req.Cour.Later && names(rel, req)
}

// numbered: the stated episode or batch range is this episode under a numbering the release may use.
func numbered(rel parse.Release, req Request) (stated, inBatch bool) {
	if !bare(rel, req) {
		stated = rel.Episode == req.Episode
		inBatch = covers(rel, req.Episode)
	}
	// Counting across cours needs this show's name: a bare "Bleach - 42" is the
	// original series'. A stated season makes the count TheTVDB's: S17E42 is ours.
	if tvdb := req.Alias.Tvdb; tvdb > 0 && (namesShow(rel, req) || broadcast(rel, req)) {
		stated = stated || rel.Episode == tvdb
		inBatch = inBatch || covers(rel, tvdb)
	}
	// An absolute number is unique across the franchise, so naming it is enough.
	if abs := req.Alias.Absolute; abs > 0 && names(rel, req) {
		stated = stated || rel.Episode == abs
		inBatch = inBatch || covers(rel, abs)
	}
	return stated, inBatch
}

// confirms reports that the release names the episode asked for, not merely that nothing contradicts it.
func confirms(rel parse.Release, req Request) bool {
	if req.Episode <= 0 {
		return false
	}
	stated, inBatch := numbered(rel, req)
	// A pack stating no range confirms nothing...
	if stated || (inBatch && rel.Episode > 0) {
		return true
	}
	// ...unless it is this whole season ("Monster.2004.S01"); its file list is
	// checked before download. Not for a later cour, numbered differently.
	return rel.Batch && rel.Episode == 0 && rel.Season > 0 && rel.Season == req.Season && !req.Cour.Later
}

// verifies rejects the demonstrably wrong episode, cour or season. A release
// that omits the season is allowed: most single-season shows never mention one.
func verifies(rel parse.Release, req Request) bool {
	// Named for another cour of the same show: a different episode, whatever its number.
	own, sibling := marked(rel, req)
	if sibling && !own {
		return false
	}
	// Cours reuse numbers, so a disagreeing stated part is another cour's same-numbered episode.
	if req.Part > 0 && rel.Part > 0 && rel.Part != req.Part {
		return false
	}

	if req.Episode > 0 {
		stated, inBatch := numbered(rel, req)

		// A pack stating no episode or "batch" is settled by its file list, under this cour's name only.
		unknownScope := rel.Episode == 0 && !rel.Batch &&
			len(numbersFor(rel, req)) > 0 && (own || namesShow(rel, req))

		if !stated && !inBatch && !unknownScope {
			return false
		}
	}
	// Its stated season is TheTVDB's: The Calamity's episode 2 ships as S17E42.
	if req.Alias.Tvdb > req.Episode && rel.Episode == req.Alias.Tvdb && broadcast(rel, req) {
		return true
	}
	if req.Season > 1 && rel.Season > 0 && rel.Season != req.Season {
		return false
	}
	// "Overlord IV - 05" is season four's, whichever season asks.
	if n := numeralSeason(rel, req); n > 0 && n != max(req.Season, 1) {
		return false
	}
	// Season 1 releases usually carry no marker, so only an explicit higher season contradicts the request.
	if req.Season == 1 && rel.Season > 1 && rel.Season != req.Counted {
		return false
	}
	return true
}

// Latin filenames only (CJK/Cyrillic synonyms burn requests); generous, as queries run concurrently.
const maxSearchTerms = 8

var (
	// Trackers match tokens and filenames lack a title's punctuation, as Prowlarr's Nyaa strips it.
	queryPunct  = regexp.MustCompile(`[?!:;,"'’“”~*<>|/\\()\[\]{}]+`)
	querySeason = regexp.MustCompile(`(?i)\s*\b(?:S0*1|Season\s*1|1st\s+Season)\b`)
	querySpace  = regexp.MustCompile(`\s{2,}`)
)

// cleanQuery turns a catalogue title into something a tracker can match. The
// season-one marker and stray punctuation are dropped; they match nothing.
func cleanQuery(title string) string {
	out := queryPunct.ReplaceAllString(title, " ")
	out = querySeason.ReplaceAllString(out, "")
	out = strings.ReplaceAll(out, ".", " ")
	out = querySpace.ReplaceAllString(out, " ")
	return strings.TrimSpace(strings.Trim(out, "-– "))
}

func searchTerms(titles []string, english string, episode, tvdb int, bestGroups []string, dub bool, series ...string) []string {
	var latin []string
	seen := map[string]struct{}{}

	add := func(t string) {
		t = cleanQuery(t)
		if len(t) < 3 || !mostlyLatin(t) {
			return
		}
		key := strings.ToLower(t)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		latin = append(latin, t)
	}
	for _, t := range titles {
		add(t)
	}
	if len(latin) == 0 {
		return nil
	}

	terms := make([]string, 0, maxSearchTerms)
	push := func(t string) {
		if len(terms) < maxSearchTerms {
			terms = append(terms, t)
		}
	}

	// Curated group first, paired with the shortest title: a curated release may be
	// English while the primary title is romaji, and a wrong pairing matches nothing.
	for _, g := range bestGroups {
		if g = strings.TrimSpace(g); g != "" && mostlyLatin(g) {
			push(fmt.Sprintf("%s %s", g, shortest(latin)))
			break
		}
	}

	// The episode number pairs with both naming conventions: release groups
	// split between romaji and English, and searching one misses the other.
	if episode > 0 {
		push(fmt.Sprintf("%s %02d", latin[0], episode))
		// Dubs are rare among subs and seldom on a bare query's first page, so asked for by name.
		if dub {
			push(fmt.Sprintf("%s %02d dub", latin[0], episode))
			push(fmt.Sprintf("%s %02d dual audio", latin[0], episode))
		}
		if e := cleanQuery(english); e != "" && mostlyLatin(e) &&
			!strings.EqualFold(e, latin[0]) {
			push(fmt.Sprintf("%s %02d", e, episode))
			if dub {
				push(fmt.Sprintf("%s %02d dub", e, episode))
			}
		}

		// Groups dropping the cour name count through the cours, so the base name goes with that number.
		for _, t := range append([]string{english}, latin...) {
			base := cleanQuery(baseTitle(strings.TrimSpace(t)))
			if base == "" || !mostlyLatin(base) {
				continue
			}
			key := strings.ToLower(base + " ep")
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			if tvdb > 0 && tvdb != episode {
				push(fmt.Sprintf("%s %02d", base, tvdb))
			}
			push(fmt.Sprintf("%s %02d", base, episode))
		}
	}
	// Before the film's own titles, which are many: the series name is what its batches are filed under.
	for _, name := range series {
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup || key == "" {
			continue
		}
		seen[key] = struct{}{}
		push(name)
	}
	for _, t := range latin {
		push(t)
	}
	return terms
}

var courSuffix = regexp.MustCompile(
	`(?i)\s+(part|season|cour|final season)\s*\d*$|(?i)\s+\d(nd|rd|th|st)\s+season$`)

// baseTitle drops a trailing cour or season marker. Split on the last separator:
// the first would reduce "BLEACH: ... - The Calamity" to just "BLEACH".
func baseTitle(title string) string {
	if trimmed := courSuffix.ReplaceAllString(title, ""); trimmed != title {
		return strings.TrimSpace(trimmed)
	}
	for _, sep := range []string{" - ", " – "} {
		if i := strings.LastIndex(title, sep); i > 8 {
			return strings.TrimSpace(title[:i])
		}
	}
	return ""
}

// The shortest title is the most likely substring of any release name.
func shortest(titles []string) string {
	best := titles[0]
	for _, t := range titles[1:] {
		if len(t) < len(best) {
			best = t
		}
	}
	return best
}

func mostlyLatin(s string) bool {
	var latin, total int
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsDigit(r) {
			continue
		}
		total++
		if r < unicode.MaxASCII || unicode.Is(unicode.Latin, r) {
			latin++
		}
	}
	return total == 0 || float64(latin)/float64(total) >= 0.8
}
