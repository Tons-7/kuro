package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

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
	// Whether this machine transcodes in real time; atomic because ffmpeg can
	// arrive mid-session while searches run.
	hwTranscode atomic.Bool
}

// NewFinder searches idx, which is nil when no site is configured.
func NewFinder(s *store.Store, idx indexer.Source, log *slog.Logger) *Finder {
	return &Finder{store: s, indexer: idx, log: log}
}

// ErrNoIndexers is the answer to any search while no site is configured.
var ErrNoIndexers = errors.New(
	"no release sources configured — add [[indexer]] blocks to config.toml and restart")

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

	// Part is the cour this entry is, taken from its own title. Releases for a
	// different cour reuse the same episode numbers.
	Part int

	Cour Cour

	Prefs score.Preferences
}

// Cour tells one split-cour entry from the others of the same show, which
// restart their numbering under the same base title.
type Cour struct {
	// Own are words only this entry's titles carry ("kashin", "calamity");
	// Siblings are words only one other cour's carry ("soukoku", "conflict").
	Own      []string
	Siblings []string

	// Later marks an entry that is not the first cour. A release naming no
	// cour counts from the first, so its bare number is some other episode.
	Later bool

	// Bases are the word sets of this entry's titles without their cour
	// subtitle, what a release counting across cours is named by.
	Bases [][]string
}

type Candidates struct {
	Queries []string       `json:"queries"`
	Results []score.Result `json:"results"`
	Best    *score.Result  `json:"best,omitempty"`
}

// Find searches each of the show's own titles, then keeps only releases that
// verify as the requested episode of the requested season.
func (f *Finder) Find(ctx context.Context, req Request) (Candidates, error) {
	titles, err := f.store.SearchTitles(ctx, req.AnimeID)
	if err != nil {
		return Candidates{}, err
	}
	total, _ := f.store.EpisodeCount(ctx, req.AnimeID)
	bestByHash, bestByGroup, _ := f.store.SeaDexFor(ctx, req.AnimeID)

	// Results rank by seeders and the curated release is rarely the most
	// seeded, so it has to be asked for by name rather than waited for.
	groups, _ := f.store.BestGroups(ctx, req.AnimeID)
	english, _ := f.store.EnglishTitle(ctx, req.AnimeID)
	req = f.numbering(ctx, req, titles, english)
	req.Prefs.HardwareTranscode = f.hwTranscode.Load()
	queries := searchTerms(titles, english, req.Episode, req.Alias.Tvdb, groups, req.Prefs.Audio == "dub")

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
			if !verifies(rel, req) {
				continue
			}
			c := score.Candidate{
				Torrent: t, Release: rel, TotalEpisodes: total,
				Confirmed: confirms(rel, req), Numbers: numbersFor(rel, req),
				// Kept, not dropped: the picker shows what was found and why not.
				WrongShow: !identity.matches(rel, t.Title),
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
	if best, ok := score.Best(cands, req.Prefs); ok {
		out.Best = &best
	}
	return out, nil
}

func countResults(batches []searchBatch) int {
	var n int
	for _, b := range batches {
		n += len(b.results)
	}
	return n
}

// EpisodeNumbers reports which episodes releases exist for. It is the only
// evidence for a show no metadata source has catalogued.
func (f *Finder) EpisodeNumbers(ctx context.Context, animeID int) ([]int, error) {
	found, err := f.Find(ctx, Request{AnimeID: animeID})
	if err != nil {
		return nil, err
	}

	seen := map[int]struct{}{}
	for _, r := range found.Results {
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
		// Counted in characters, not bytes: a two-character Japanese word is
		// six bytes and would pass as a distinctive term.
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

// numbering fills in the cour and the alternate episode number: catalogues
// continue from the previous cour, release groups restart at one.
func (f *Finder) numbering(ctx context.Context, req Request, titles []string, english string) Request {
	for _, t := range append([]string{english}, titles...) {
		if n := parse.PartOf(t); n > 0 {
			req.Part = n
			break
		}
	}

	// A caller that named no season gets the one the title states, or the first.
	// Otherwise episode 1 would accept an "S02E01" release, a different episode.
	if req.Season == 0 {
		req.Season = 1
		for _, t := range append([]string{english}, titles...) {
			if n := parse.SeasonOf(t); n > 0 {
				req.Season = n
				break
			}
		}
	}

	req.Alias, _ = f.store.EpisodeAlias(ctx, req.AnimeID, req.Episode)
	if !req.Alias.Stated {
		req.Alias = f.derivedAlias(ctx, req, titles, english)
	}
	req.Cour = f.cour(ctx, req, titles, english)
	return req
}

// derivedAlias counts the episode through the franchise, since catalogues no
// longer carry TheTVDB's numbering: the earlier cours of this run give the
// season's count, every earlier entry the absolute.
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
		// A run broken by another entry restarts somewhere unknown; a guessed
		// number is some other episode.
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

// cour works out what separates this entry's releases from a sibling cour's,
// from the franchise the relations graph placed it in. Without relations the
// entry's own title and numbering are all there is to go on.
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

	// A word in one sibling only names that cour; one several share ("TYBW")
	// names the show.
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
		// An earlier series named by this entry's base title is the cour the
		// bare name belongs to.
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

// searchAll runs the variants together, returning them in request order: that
// order encodes which title release groups most likely used.
func (f *Finder) searchAll(ctx context.Context, source indexer.Source, queries []string) []searchBatch {
	out := make([]searchBatch, len(queries))
	slots := make(chan struct{}, searchConcurrency)

	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			// Category empty: each source knows its own, and nyaa's numbering
			// means something else on sukebei.
			results, err := source.Search(ctx, indexer.Query{Text: q})
			out[i] = searchBatch{query: q, results: results, err: err}
		}()
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
		(req.Season > 1 && rel.Season == req.Season)
	return own, sibling
}

// bare is a release for a later cour that names no cour: it counts from the
// first cour, so its episode number is not this entry's.
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

// covers reports that a batch's stated range holds the number; a batch stating
// no range may hold anything.
func covers(rel parse.Release, n int) bool {
	return rel.Batch && rel.Episode <= n && (rel.EpisodeEnd == 0 || n <= rel.EpisodeEnd)
}

// namesShow reports that a release names this entry without its cour: at
// least half the words of one of its base titles. "Bleach - 45" is the
// original series' episode 45, not Thousand-Year Blood War's.
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

// names reports that the release is this show's, either by naming enough of a
// base title or by shortening one: "Bleach" for Bleach TYBW.
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

// broadcast reports a later cour's release stating the season TheTVDB files it
// under, in this show's name.
func broadcast(rel parse.Release, req Request) bool {
	return rel.Season > 1 && req.Cour.Later && names(rel, req)
}

// numbered reports that the release's stated episode, or stated batch range,
// is this episode under some numbering the release may be using.
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

// confirms reports that the release names the episode asked for, rather than
// merely failing to contradict it.
func confirms(rel parse.Release, req Request) bool {
	if req.Episode <= 0 {
		return false
	}
	stated, inBatch := numbered(rel, req)
	return stated || inBatch
}

// verifies rejects the demonstrably wrong episode, cour or season. A release
// that omits the season is allowed: most single-season shows never mention one.
func verifies(rel parse.Release, req Request) bool {
	// Named for another cour of the same show: a different episode, whatever
	// its number.
	own, sibling := marked(rel, req)
	if sibling && !own {
		return false
	}
	// Cours reuse each other's episode numbers, so a stated part that
	// disagrees is a different show's episode with the same number.
	if req.Part > 0 && rel.Part > 0 && rel.Part != req.Part {
		return false
	}

	if req.Episode > 0 {
		stated, inBatch := numbered(rel, req)

		// Many season packs state neither an episode number nor "batch": scope
		// is unknown, and the file list settles it later — under this cour's or
		// show's name only, or the original series' file 42 passes for ours.
		unknownScope := rel.Episode == 0 && !rel.Batch &&
			len(numbersFor(rel, req)) > 0 && (own || namesShow(rel, req))

		if !stated && !inBatch && !unknownScope {
			return false
		}
	}
	// The season such a release states is TheTVDB's, not the entry's: The
	// Calamity's episode 2 ships as S17E42.
	if req.Alias.Tvdb > req.Episode && rel.Episode == req.Alias.Tvdb && broadcast(rel, req) {
		return true
	}
	if req.Season > 1 && rel.Season > 0 && rel.Season != req.Season {
		return false
	}
	// Season 1 releases usually carry no marker, so only an explicit higher
	// season contradicts the request.
	if req.Season == 1 && rel.Season > 1 {
		return false
	}
	return true
}

// Latin filenames only, so CJK/Cyrillic synonyms just burn requests. Generous
// because the queries run concurrently.
const maxSearchTerms = 8

var (
	// Trackers match on tokens, and a title's punctuation is never in the
	// filename. Prowlarr's own Nyaa definition strips the same things.
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

func searchTerms(titles []string, english string, episode, tvdb int, bestGroups []string, dub bool) []string {
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
		// A dub is a few releases among hundreds of subs, rarely inside the
		// newest page a bare query returns; asked for by name instead.
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

		// The catalogue name found one release with seven seeders; the same
		// name without its cour subtitle found the episode with hundreds. A
		// group dropping the cour name usually counts through the cours too,
		// so that number goes with the base name first.
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
