package parse

import (
	"regexp"
	"strings"
)

// Subtitle languages read from the release name — the only pre-download evidence.
// Hardsub is tracked separately: a burnt-in wrong-language track is unwatchable.
var subtitleMarkers = []struct {
	lang string
	re   *regexp.Regexp
}{
	// "Multi-Subs" without a list of languages effectively always includes
	// English, which is the only reason a group advertises it.
	{"en", regexp.MustCompile(`(?i)\b(?:eng(?:lish)?)[\s._-]*(?:sub(?:s|bed|title)?s?)\b` +
		`|\bsub(?:s|title)?s?[\s._-]*\(?\s*eng?\b` +
		`|\[eng?(?:lish)?\]` +
		`|\bmulti[\s._-]*(?:sub|subtitle)s?\b|\bmultiple[\s._-]*subtitles?\b`)},

	// 字幕组 is "subtitle group", 内嵌/内封 are hard/softsub. Match simplified and
	// traditional markers, not bare Han characters — a Japanese title is full of kanji.
	{"zh", regexp.MustCompile(`简体|简繁|繁体|繁體|简日|繁日|中文|字幕组|双语|简中|繁中` +
		`|(?i)\b(?:chs|cht|big5|gb_?jp|chinese)\b`)},

	{"es", regexp.MustCompile(`(?i)\b(?:spa(?:nish)?|castellano|espa[nñ]ol|latino)[\s._-]*sub|\[esp?\]`)},
	{"pt", regexp.MustCompile(`(?i)\blegendado\b|\bpt-?br\b|\bportugu[eê]s\b`)},
	{"ru", regexp.MustCompile(`(?i)\brus(?:sian)?\b|русск|\[ru\]`)},
	{"ar", regexp.MustCompile(`(?i)\barabic\b|\bmutamer\b|مترجم`)},
	{"id", regexp.MustCompile(`(?i)\bindo(?:nesian?)?[\s._-]*sub\b`)},
	{"vi", regexp.MustCompile(`(?i)\bviet(?:sub|namese)\b`)},
	{"th", regexp.MustCompile(`(?i)\bthai[\s._-]*sub\b`)},
	{"ko", regexp.MustCompile(`(?i)\bkor(?:ean)?[\s._-]*sub\b|한국어`)},
	{"fr", regexp.MustCompile(`(?i)\b(?:vostfr|french|fre)[\s._-]*sub\b|\bvostfr\b`)},
	{"de", regexp.MustCompile(`(?i)\bger(?:man)?[\s._-]*sub\b|\bdeutsch\b`)},
	{"it", regexp.MustCompile(`(?i)\bita(?:lian)?[\s._-]*sub\b`)},
}

// 内嵌 and 硬字幕 mean burnt into the picture; "hardsub" is the western spelling
// of the same thing.
var hardSubbed = regexp.MustCompile(`内嵌|硬字幕|(?i)\bhard[\s._-]?sub(?:bed|s)?\b`)

// detectSubtitles returns the languages a release name claims to carry, in the
// order listed above rather than the order they appear.
func detectSubtitles(name string) ([]string, bool) {
	var out []string
	for _, m := range subtitleMarkers {
		if m.re.MatchString(name) {
			out = append(out, m.lang)
		}
	}
	return out, hardSubbed.MatchString(name)
}

// HasSubtitleLanguage reports whether a release carries one of the given
// languages. An unlabelled release matches nothing, so callers rank rather than reject.
func HasSubtitleLanguage(r Release, langs []string) bool {
	for _, want := range langs {
		for _, got := range r.Subtitles {
			if strings.EqualFold(want, got) {
				return true
			}
		}
	}
	return false
}
