// Package film tells the films of one series apart by subtitle and chapter: each is catalogued as "episode 1".
package film

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Entry is one film, by the titles it goes by.
type Entry struct{ Titles []string }

// Key identifies one film among its series.
type Key struct {
	// Number is the chapter the film's own titles state, 0 when they state none.
	Number int
	// Count is how many films the series has, for sizing one inside a batch.
	Count int

	own    [][]string
	others [][][]string
}

// Verdict is what a name says about which film it is.
type Verdict int

const (
	// Unknown: names the series at most, as a batch of all of it does.
	Unknown Verdict = iota
	Ours
	Other
)

// New prepares the key for one film against the rest of its series.
func New(own Entry, siblings []Entry) Key {
	all := append([]Entry{own}, siblings...)

	// A word most entries share names the series, not a film.
	seen := map[string]int{}
	for _, e := range all {
		words := map[string]bool{}
		for _, t := range e.Titles {
			for _, w := range distinctive(t) {
				words[w] = true
			}
		}
		for w := range words {
			seen[w]++
		}
	}
	common := func(w string) bool { return seen[w] >= 2 && seen[w]*2 >= len(all) }

	sets := func(e Entry) [][]string {
		var out [][]string
		for _, t := range e.Titles {
			words := slices.DeleteFunc(distinctive(t), common)
			if len(words) > 0 {
				out = append(out, words)
			}
		}
		return out
	}

	k := Key{Count: len(all), own: sets(own)}
	for _, t := range own.Titles {
		if n := stated(t); n > 0 {
			k.Number = n
			break
		}
	}
	for _, s := range siblings {
		k.others = append(k.others, sets(s))
	}
	return k
}

// Match judges a release name or a file name.
func (k Key) Match(name string) Verdict {
	v, _ := k.judge(name)
	return v
}

// Named reports a name carrying this film's title words; a number alone varies by group and confirms nothing.
func (k Key) Named(name string) bool {
	v, grade := k.judge(name)
	return v == Ours && grade >= 2
}

// judge also grades the evidence: 2 for the film's words, plus 1 for an agreeing number.
func (k Key) judge(name string) (Verdict, int) {
	words := tokens(name)
	ours := fit(k.own, words)
	var theirs evidence
	for _, o := range k.others {
		if e := fit(o, words); e.beats(theirs) {
			theirs = e
		}
	}
	num, fractional := Number(name)
	agrees := k.Number > 0 && num == k.Number
	disagrees := k.Number > 0 && num > 0 && num != k.Number

	switch {
	case ours.hits > 0 && ours.beats(theirs):
		if agrees {
			return Ours, 3
		}
		return Ours, 2
	case theirs.beats(ours):
		return Other, 0
	case ours.hits > 0:
		// Two films share the subtitle ("Murder Speculation" A and B); only the number separates them.
		if agrees {
			return Ours, 3
		}
		return Other, 0
	case fractional || disagrees:
		// "06.5" is an extra between two films; another number is another film.
		return Other, 0
	case agrees:
		return Ours, 1
	}
	return Unknown, 0
}

// File is what Pick needs of a torrent's file.
type File struct {
	Name   string
	Length int64
}

// Pick finds this film among a release's files; among several one must stand out, or a mislabelled batch wins.
func (k Key) Pick(files []File) (int, bool) {
	var videos []int
	for i, f := range files {
		if IsVideo(f.Name) {
			videos = append(videos, i)
		}
	}
	switch len(videos) {
	case 0:
		return -1, false
	case 1:
		return videos[0], k.Match(baseName(files[videos[0]].Name)) != Other
	}

	// Trailers and cinema intros are numbered like the films; they are a fraction of the size.
	lengths := make([]int64, 0, len(videos))
	for _, i := range videos {
		lengths = append(lengths, files[i].Length)
	}
	slices.Sort(lengths)
	floor := lengths[len(lengths)/2] / 4

	best, grade, tied := -1, 0, false
	for _, i := range videos {
		v, g := k.judge(baseName(files[i].Name))
		// A number alone is not proof for a small file; the film's own name is, whatever its length.
		if v != Ours || (g == 1 && files[i].Length < floor) {
			continue
		}
		switch {
		case g > grade:
			best, grade, tied = i, g, false
		case g == grade:
			tied = true
		}
	}
	return best, best >= 0 && !tied
}

type evidence struct {
	ratio float64
	hits  int
}

func (e evidence) beats(o evidence) bool {
	return e.ratio > o.ratio || (e.ratio == o.ratio && e.hits > o.hits)
}

// fit is how well a name carries one of an entry's titles: at least half its distinctive words.
func fit(titles [][]string, words map[string]bool) evidence {
	var best evidence
	for _, t := range titles {
		var hits int
		for _, w := range t {
			if words[w] {
				hits++
			}
		}
		e := evidence{ratio: float64(hits) / float64(len(t)), hits: hits}
		if hits > 0 && e.ratio >= 0.5 && e.beats(best) {
			best = e
		}
	}
	return best
}

var (
	split = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	// Square brackets hold tags; round ones hold tags too, unless it is just the number: "(6)".
	square = regexp.MustCompile(`\[[^\]]*\]`)
	round  = regexp.MustCompile(`\(([^)]*)\)`)
	digits = regexp.MustCompile(`^\d{1,2}$`)
	// "FLAC 2.0", "DDP5.1": an audio layout, not film 2 or 5.
	layout = regexp.MustCompile(`(?i)(?:aac|flac|ac3|e-?ac-?3|dts(?:-?hd)?|opus|ddp?|truehd|l?pcm|ma)[\s._-]*\d\.\d`)
	// "Movies 1 - 8", "01~07": a range names the whole series.
	spanned = regexp.MustCompile(`(?:^|[\s_])\d{1,2}\s*[-~]\s*\d{1,2}(?:$|[\s_])`)
	marker  = regexp.MustCompile(`(?i)\b(?:chapter|movie|film|vol(?:ume)?)\.?\s*(\d{1,2})\b`)
	// A number standing alone: "06 ", "- 6 -", "4v0", "6.0", and "6.5" with its fraction.
	alone = regexp.MustCompile(`(?:^|[\s_-])(\d{1,2})(?:\.(\d))?(?:v\d)?(?:$|[\s_:-])`)
)

var stop = map[string]bool{
	"the": true, "and": true, "with": true, "from": true, "part": true, "movie": true, "film": true,
	"chapter": true, "season": true, "gekijouban": true, "complete": true, "series": true,
}

func tokens(name string) map[string]bool {
	out := map[string]bool{}
	for _, w := range split.Split(strings.ToLower(name), -1) {
		if w != "" {
			out[w] = true
		}
	}
	return out
}

// distinctive are a title's words long enough to mean something: "no", "Zen" and "Kou" are not.
func distinctive(title string) []string {
	var out []string
	for _, w := range split.Split(strings.ToLower(title), -1) {
		if utf8.RuneCountInString(w) < 4 || stop[w] || slices.Contains(out, w) {
			continue
		}
		if _, err := strconv.Atoi(w); err == nil {
			continue
		}
		out = append(out, w)
	}
	return out
}

// stated is the chapter a catalogue title names outright: "Chapter 6".
func stated(title string) int {
	if m := marker.FindStringSubmatch(title); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// Number is the film a name is numbered as, 0 when it gives none or a range. fractional marks "6.5".
func Number(name string) (n int, fractional bool) {
	if m := marker.FindStringSubmatch(name); m != nil {
		n, _ = strconv.Atoi(m[1])
		return n, false
	}
	s := square.ReplaceAllString(name, " ")
	s = round.ReplaceAllStringFunc(s, func(group string) string {
		if inner := strings.TrimSpace(group[1 : len(group)-1]); digits.MatchString(inner) {
			return " " + inner + " "
		}
		return " "
	})
	s = layout.ReplaceAllString(s, " ")
	if spanned.MatchString(s) {
		return 0, false
	}
	m := alone.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, _ = strconv.Atoi(m[1])
	if m[2] != "" && m[2] != "0" {
		return 0, true
	}
	return n, false
}

func baseName(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.LastIndexByte(path, '.'); i > 0 {
		path = path[:i]
	}
	return path
}

// IsVideo reports a file a player can open.
func IsVideo(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".mkv", ".mp4", ".avi", ".ts", ".m2ts", ".webm", ".mov"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
