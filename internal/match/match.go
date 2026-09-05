// Package match resolves a parsed release title to a specific anime. AniList search
// degrades on specific queries, so candidates come from a local token index scored here.
package match

import (
	"sort"
	"strings"

	"kuro/internal/corpus"
)

// Weights follow Seanime's published table, which is the most battle-tested
// set in this space, with two corrections noted at their use sites.
const (
	scoreExact          = 15.0
	scoreNormalisedEq   = 14.0
	scoreTokenPerfect   = 12.0
	scoreTokenComplete  = 10.0
	scoreTokenHigh      = 8.0
	scoreTokenMedium    = 5.0
	scoreFuzzyHigh      = 6.0
	scoreFuzzyMedium    = 3.0
	scoreMainTitleBonus = 2.0

	scoreSeasonMatch    = 5.0
	scoreSeasonMismatch = -8.0
	scorePartMatch      = 3.0
	scorePartMismatch   = -5.0
	scoreFormatMatch    = 5.0
	scoreFormatMismatch = -5.0
	scoreYearExact      = 4.0
	scoreYearClose      = 2.0
	scoreYearMismatch   = -10.0

	scoreEpisodeOutOfRange = -6.0

	// Enough to settle an exact tie between franchise entries, never enough to
	// overturn a genuinely better title match.
	BoostWatching  = 4.0
	BoostPlanned   = 2.0
	BoostCompleted = 1.0

	thresholdTokenHigh   = 0.90
	thresholdTokenMedium = 0.70
	thresholdFuzzyHigh   = 0.90
	thresholdFuzzyMedium = 0.80

	// Auto-accept needs a high score and daylight over the runner-up: bare
	// "Frieren" scores identically against season 1, season 2 and the ONA.
	thresholdAccept = 6.0
	thresholdAuto   = 16.0
	thresholdMargin = 2.0

	// Season and year are only computed for candidates whose title is already
	// plausible, so a coincidental year cannot rescue an unrelated show.
	thresholdTitleFloor = 2.0
)

type Media struct {
	ID       int
	Titles   []string
	Format   string
	Episodes int
	Year     int
	Season   int
}

type Query struct {
	Title   string
	Season  int
	Part    int
	Year    int
	Format  string
	Episode int

	// Boost nudges specific anime by id: franchise seasons share one English
	// title, so titles alone can't separate them but what the user watches can.
	Boost map[int]float64
}

type Result struct {
	MediaID  int      `json:"mediaId"`
	Score    float64  `json:"score"`
	Margin   float64  `json:"margin"`
	Matched  string   `json:"matched"`
	Reasons  []string `json:"reasons"`
	Decision Decision `json:"decision"`
}

type Decision string

const (
	// Confident enough to start playing without asking.
	Auto Decision = "auto"
	// A real candidate, but close enough to the runner-up that acting risks the
	// wrong show — surface the options instead.
	Ask Decision = "ask"
)

func (r Result) Ambiguous() bool { return r.Decision == Ask }

type indexed struct {
	media  Media
	keys   []key
	tokens map[string]struct{}
}

type key struct {
	raw    string
	norm   string
	fold   string
	tokens []string
	main   bool
}

// Index holds the searchable corpus. Building it is O(titles); afterwards a
// lookup touches well under one percent of it.
type Index struct {
	media   []indexed
	byToken map[string][]int32
	byID    map[int]int32
}

func NewIndex() *Index {
	return &Index{byToken: make(map[string][]int32), byID: make(map[int]int32)}
}

func (ix *Index) Len() int { return len(ix.media) }

func (ix *Index) Add(m Media) {
	if len(m.Titles) == 0 {
		return
	}

	entry := indexed{media: m, tokens: map[string]struct{}{}}
	for i, raw := range m.Titles {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		k := key{
			raw:  strings.ToLower(raw),
			norm: corpus.Normalise(raw),
			fold: corpus.Fold(raw),
			main: i == 0,
		}
		k.tokens = significant(k.norm)
		entry.keys = append(entry.keys, k)
		for _, tok := range k.tokens {
			entry.tokens[tok] = struct{}{}
		}
	}
	if len(entry.keys) == 0 {
		return
	}

	pos := int32(len(ix.media))
	ix.media = append(ix.media, entry)
	ix.byID[m.ID] = pos

	for tok := range entry.tokens {
		ix.byToken[tok] = append(ix.byToken[tok], pos)
	}
	// Compounds let "rezero" reach "re zero kara hajimeru", which release
	// groups write both ways.
	for _, k := range entry.keys {
		for i := 0; i+1 < len(k.tokens); i++ {
			if len(k.tokens[i]) <= 5 && len(k.tokens[i+1]) <= 5 {
				c := k.tokens[i] + k.tokens[i+1]
				ix.byToken[c] = append(ix.byToken[c], pos)
			}
		}
	}
}

func (ix *Index) Match(q Query) (Result, bool) {
	ranked := ix.Rank(q, 5)
	if len(ranked) == 0 || ranked[0].Score < thresholdAccept {
		return Result{}, false
	}

	best := ranked[0]
	best.Margin = best.Score
	if len(ranked) > 1 {
		best.Margin = best.Score - ranked[1].Score
	}

	best.Decision = Ask
	if best.Score >= thresholdAuto && best.Margin >= thresholdMargin {
		best.Decision = Auto
	}
	return best, true
}

func (ix *Index) Rank(q Query, n int) []Result {
	qn := corpus.Normalise(q.Title)
	if qn == "" {
		return nil
	}
	qf := corpus.Fold(q.Title)
	qt := significant(qn)

	results := make([]Result, 0, 16)
	for _, pos := range ix.candidates(qt) {
		entry := ix.media[pos]
		if r, ok := score(q, qn, qf, qt, entry); ok {
			results = append(results, r)
		}
	}

	// Quantise before sorting. Comparing floats with a tolerance is not a
	// strict weak ordering and makes sort.Slice return arbitrary results.
	sort.Slice(results, func(i, j int) bool {
		a, b := int64(results[i].Score*1000), int64(results[j].Score*1000)
		if a != b {
			return a > b
		}
		return results[i].MediaID < results[j].MediaID
	})

	if n > 0 && len(results) > n {
		results = results[:n]
	}
	return results
}

// candidates unions the posting lists of the query's tokens. An empty union is
// a genuine miss, not a reason to scan the whole corpus.
func (ix *Index) candidates(tokens []string) []int32 {
	seen := make(map[int32]struct{}, 256)
	out := make([]int32, 0, 256)
	for _, tok := range tokens {
		for _, pos := range ix.byToken[tok] {
			if _, dup := seen[pos]; dup {
				continue
			}
			seen[pos] = struct{}{}
			out = append(out, pos)
		}
	}
	return out
}

func score(q Query, qn, qf string, qt []string, entry indexed) (Result, bool) {
	best := -1.0
	var matched string
	var reason string

	for _, k := range entry.keys {
		s, why := titleScore(q.Title, qn, qf, qt, k)
		if k.main {
			s += scoreMainTitleBonus
		}
		if s > best {
			best, matched, reason = s, k.raw, why
		}
	}

	if best < thresholdTitleFloor {
		return Result{}, false
	}

	r := Result{MediaID: entry.media.ID, Score: best, Matched: matched}
	r.Reasons = append(r.Reasons, reason)

	// A stronger title match earns more trust in the metadata signals.
	trust := best / 6.0
	if trust > 1 {
		trust = 1
	}

	m := entry.media
	if q.Season > 0 || m.Season > 0 {
		switch {
		case q.Season == m.Season && q.Season > 0:
			r.Score += scoreSeasonMatch
			r.Reasons = append(r.Reasons, "season matches")
		case q.Season > 0 && m.Season > 0 && q.Season != m.Season:
			r.Score += scoreSeasonMismatch
			r.Reasons = append(r.Reasons, "different season")
		}
	}

	if q.Part > 0 && m.Season > 0 {
		if q.Part == m.Season {
			r.Score += scorePartMatch
		} else {
			r.Score += scorePartMismatch
		}
	}

	if q.Format != "" && m.Format != "" {
		if strings.EqualFold(q.Format, m.Format) {
			r.Score += scoreFormatMatch
		} else {
			r.Score += scoreFormatMismatch
			r.Reasons = append(r.Reasons, "different format")
		}
	}

	if q.Year > 0 && m.Year > 0 {
		switch d := q.Year - m.Year; {
		case d == 0:
			r.Score += scoreYearExact * trust
			r.Reasons = append(r.Reasons, "year matches")
		case d == 1 || d == -1:
			r.Score += scoreYearClose * trust
		default:
			r.Score += scoreYearMismatch * trust
			r.Reasons = append(r.Reasons, "year is far off")
		}
	}

	// An episode number beyond what a season contains rules it out, which is
	// what separates franchise entries sharing one English title.
	if q.Episode > 0 && m.Episodes > 0 && q.Episode > m.Episodes {
		r.Score += scoreEpisodeOutOfRange
		r.Reasons = append(r.Reasons, "episode number exceeds this entry")
	}

	if boost := q.Boost[m.ID]; boost != 0 {
		r.Score += boost
		r.Reasons = append(r.Reasons, "on your list")
	}

	return r, true
}

func titleScore(rawQuery, qn, qf string, qt []string, k key) (float64, string) {
	if strings.EqualFold(rawQuery, k.raw) {
		return scoreExact + complexity(qt), "exact title"
	}
	if qn == k.norm {
		return scoreNormalisedEq + complexity(qt), "identical after normalisation"
	}
	if qf != "" && qf == k.fold {
		return scoreNormalisedEq + complexity(qt) - 1, "identical after folding romanisation"
	}

	shared, cover := tokenOverlap(qt, k.tokens)
	switch {
	case cover == 1 && len(qt) == len(k.tokens) && len(qt) > 0:
		return scoreTokenPerfect + complexity(qt), "every word matches"
	case cover == 1:
		// Scaled by coverage: a flat score ties every franchise entry, e.g.
		// "Boku wa Hero" and "Boku no Hero Academia".
		ratio := float64(min(len(qt), len(k.tokens))) / float64(max(len(qt), len(k.tokens)))
		return scoreTokenComplete * ratio, "all words present"
	case shared >= thresholdTokenHigh:
		return scoreTokenHigh, "most words match"
	case shared >= thresholdTokenMedium:
		return scoreTokenMedium, "several words match"
	}

	switch d := dice(qn, k.norm); {
	case d >= thresholdFuzzyHigh:
		return scoreFuzzyHigh + (d-thresholdFuzzyHigh)*5, "very similar spelling"
	case d >= thresholdFuzzyMedium:
		return scoreFuzzyMedium + (d-thresholdFuzzyMedium)*5, "similar spelling"
	default:
		return d, "weak similarity"
	}
}

// Longer titles matching in full is far stronger evidence than a single word,
// so length is rewarded rather than treated as neutral.
func complexity(tokens []string) float64 {
	switch {
	case len(tokens) >= 3:
		return 3
	case len(tokens) == 2:
		return 1
	default:
		return 0
	}
}

func tokenOverlap(a, b []string) (ratio float64, coverage float64) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0
	}
	set := make(map[string]struct{}, len(b))
	for _, t := range b {
		set[t] = struct{}{}
	}

	var hits int
	for _, t := range a {
		if _, ok := set[t]; ok {
			hits++
		}
	}
	if hits == len(a) || hits == len(b) {
		coverage = 1
	}
	return float64(hits) / float64(max(len(a), len(b))), coverage
}

// Single characters carry no signal and blow up the posting lists.
func significant(norm string) []string {
	fields := strings.Fields(norm)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) > 1 && !noise[f] {
			out = append(out, f)
		}
	}
	return out
}

var noise = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "and": true,
	"no": true, "wa": true, "ga": true, "wo": true, "ni": true, "de": true,
	"tv": true, "season": true, "part": true, "cour": true,
}
