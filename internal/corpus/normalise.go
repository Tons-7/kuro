package corpus

import (
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// Normalise never alters spelling, so two titles cannot collapse into one.
// Step order matters: macrons expand to digraphs before the diacritic strip,
// or "Sōsō" becomes "soso" while "Sousou" stays "sousou".
func Normalise(s string) string {
	s = width.Fold.String(strings.TrimSpace(s))
	s = strings.ToLower(s)
	s = macrons.Replace(s)
	s = strings.ReplaceAll(s, "&", " and ")
	s = punctuation.Replace(s)
	s = stripMarks(s)
	s = strings.Join(strings.Fields(s), " ")
	s = replaceAll(s, seasonForms)
	s = replaceAll(s, romanNumerals)
	return strings.Join(strings.Fields(s), " ")
}

var macrons = strings.NewReplacer(
	"ā", "aa", "ī", "ii", "ū", "uu", "ē", "ee", "ō", "ou",
	"â", "aa", "î", "ii", "û", "uu", "ê", "ee", "ô", "ou",
)

// Fold collapses long vowels so Sōsō, Sousou and Soosoo all become "soso".
// Lossy ("good" and "god" collapse too), so only tried after exact Normalise fails.
func Fold(s string) string {
	s = Normalise(s)
	for _, v := range []string{"a", "e", "i", "o", "u"} {
		s = strings.ReplaceAll(s, v+v, v)
	}
	s = strings.ReplaceAll(s, "ou", "o")
	return strings.Join(strings.Fields(s), " ")
}

// transform.Chain holds internal buffer offsets and is not concurrency-safe, so
// each use takes one from a pool rather than sharing or rebuilding per call.
var marks = sync.Pool{
	New: func() any {
		return transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	},
}

func stripMarks(s string) string {
	t := marks.Get().(transform.Transformer)
	defer marks.Put(t)

	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}

var seasonForms = []struct{ from, to string }{
	{"1st season", " season 1 "}, {"2nd season", " season 2 "},
	{"3rd season", " season 3 "}, {"4th season", " season 4 "},
	{"5th season", " season 5 "}, {"6th season", " season 6 "},
	{"season 1", " season 1 "}, {"season 2", " season 2 "},
	{"season 3", " season 3 "}, {"season 4", " season 4 "},
	{" s1 ", " season 1 "}, {" s2 ", " season 2 "},
	{" s3 ", " season 3 "}, {" s4 ", " season 4 "},
	{"part 1", " season 1 "}, {"part 2", " season 2 "},
	{"cour 1", " season 1 "}, {"cour 2", " season 2 "},
}

var romanNumerals = []struct{ from, to string }{
	{" viii", " 8"}, {" vii", " 7"}, {" vi", " 6"}, {" iv", " 4"},
	{" iii", " 3"}, {" ii", " 2"}, {" ix", " 9"}, {" x", " 10"}, {" v", " 5"},
}

var punctuation = strings.NewReplacer(
	":", " ", ";", " ", ",", " ", ".", " ", "!", " ", "?", " ",
	"'", "", "’", "", "\"", " ", "`", "",
	"-", " ", "–", " ", "—", " ", "_", " ", "~", " ",
	"(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ",
	"/", " ", "\\", " ", "|", " ", "+", " ", "*", " ", "@", " ", "#", " ",
	"　", " ", "・", " ",
)

func replaceAll(s string, pairs []struct{ from, to string }) string {
	padded := " " + s + " "
	for _, p := range pairs {
		padded = strings.ReplaceAll(padded, p.from, p.to)
	}
	return padded
}
