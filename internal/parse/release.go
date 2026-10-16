// Package parse reads release filenames by fansub convention: "[SubsPlease] Frieren - 10 (1080p) [7D35515E].mkv".
package parse

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type Release struct {
	// Raw is the original filename. Tags are stripped from Title, so anything
	// looking for a marker like "Dual Audio" has to read this instead.
	Raw    string
	Title  string
	Group  string
	Season int
	// Part separates the cours of one season, which restart their numbering.
	// A Part 3 release is not a Part 2 release at the same episode number.
	Part       int
	Episode    int
	EpisodeEnd int
	Version    int
	Resolution string
	Source     string
	Codec      string
	BitDepth   int
	Audio      []string
	DualAudio  bool
	// Subtitles are the languages the name claims; empty means unlabelled, not absent.
	Subtitles []string
	HardSub   bool
	Batch     bool
	CRC32     string
	Extension string
}

var (
	bracketed  = regexp.MustCompile(`[\[\(]([^\]\)]*)[\]\)]`)
	leadGroup  = regexp.MustCompile(`^\[([^\]]+)\]`)
	sceneGroup = regexp.MustCompile(`-([A-Za-z0-9]+)$`)
	crc32Re    = regexp.MustCompile(`(?i)^[0-9A-F]{8}$`)
	// Also glued to the source: "BD1080p", "WEB_1080p".
	resolution = regexp.MustCompile(`(?i)(?:\b|_|BD|WEB|DVD|HDTV|Rip)(\d{3,4})[pi]\b|\b(\d{3,4})x(\d{3,4})\b`)
	seasonEp   = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*E(\d{1,4})\b`)
	// "S01E01-E12", "S01E01-12": a pack spelled as a span of the marked form.
	seasonEpSpan = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*E(\d{1,4})\s*[-~]\s*(?:S\d{1,2})?E?(\d{1,4})\b`)
	// "[05]", "[05v2]": a number alone in its own bracket, as Chinese groups write the episode.
	bracketEp = regexp.MustCompile(`\[(\d{1,4})(?:v(\d))?\]`)
	dashEp    = regexp.MustCompile(`(?:^|\s)-\s*(\d{1,4})(?:v(\d))?(?:\s|$)`)
	rangeEp   = regexp.MustCompile(`(?:^|[\s\(\[])(\d{1,4})\s*[-~]\s*(\d{1,4})(?:[\s\)\]]|$)`)
	// "E06", "EP06", "Ep. 6"; the leading boundary keeps it out of checksums and codecs.
	markedEp = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])E(?:P|PISODE)?\.?\s*(\d{1,4})(?:v(\d))?\b`)
	// The same form listed rather than ranged: "[E41 E42 E43]".
	listedEp = regexp.MustCompile(`(?i)\bE(\d{1,4})(?:[\s,]+E(\d{1,4}))+\b`)
	// "S02SP01-02" has no boundary after the season, so specials get their own form.
	seasonOnly = regexp.MustCompile(`(?i)\b(?:S(\d{1,2})|Season\s*(\d{1,2}))\b|\b(\d)(?:nd|rd|th)\s*Season\b|\bS(\d{1,2})SP\d{1,4}(?:\s*-\s*\d{1,4})?\b`)
	partOnly   = regexp.MustCompile(`(?i)\b(?:part|cour)\s*(\d{1,2}|IV|I{1,3})\b`)
	bitDepthRe = regexp.MustCompile(`(?i)\b(8|10)\s*-?\s*bits?\b|\bhi(10)p\b`)
	extRe      = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|ts|m2ts|webm)$`)

	// Where the title stops and quality metadata begins, for scene-style names with no brackets to strip.
	metaTokens = `\d{3,4}[pi]|BD|BDRip|BDMV|Blu-?ray|Remux|WEB-?DL|WEB-?Rip|WEB|HDTV|TVRip|DVD|DVDRip|CR|HIDIVE|AMZN|NF|Baha|Funi|x26[45]|H\.?26[45]|HEVC|AVC|AV1|VP9|MPEG-?2|AAC|FLAC|Opus|E?-?AC-?3|DDP?|DTS|TrueHD|Dual|Multi|10-?bits?|8-?bits?|Hi10P|Complete|Batch`
	metaStart  = regexp.MustCompile(`(?i)[\s._]+\b(` + metaTokens + `)\b`)

	// A bare number counts before the quality run, after a word, and before a bracket only with two digits.
	bareEp = regexp.MustCompile(`(?i)(?:^|[\s._])(?:(\d{2,3})(?:v(\d))?[\s._]+|(\d{2})(?:v(\d))?[\s._]*\[)(` + metaTokens + `)\b`)

	dotSeparated = regexp.MustCompile(`[._]`)

	// A finale marker after the number: "Show - 12 END".
	endAfterEp = regexp.MustCompile(`(?i)((?:^|\s)-\s*\d{1,4}(?:v\d)?)\s+(?:end|final)\b`)
)

type named struct {
	re   *regexp.Regexp
	name string
}

func table(entries ...string) []named {
	out := make([]named, 0, len(entries)/2)
	for i := 0; i+1 < len(entries); i += 2 {
		out = append(out, named{regexp.MustCompile(entries[i]), entries[i+1]})
	}
	return out
}

// Source is checked longest-first so "BD Remux" is not swallowed by "BD".
var sources = table(
	`bd\s*remux|blu-?ray\s*remux|remux`, "BDRemux",
	`\bbdrip\b|\bbd(?:_?\d{3,4}[pi])?\b|blu-?ray|\bbdmv\b`, "BD",
	`\bdvdrip\b|\bdvd\b`, "DVD",
	`web-?dl|web-?rip|\bweb(?:_?\d{3,4}[pi])?\b|\bcr\b|hidive|\bamzn\b|\bnf\b|\bbaha\b|funi`, "WEB",
	`\bhdtv\b|\btvrip\b|\bbs11\b|\bbs\d{1,2}\b|\btx\b`, "TV",
)

var dualAudio = regexp.MustCompile(`(?i)\b(?:dual[\s-]?audio|multi[\s-]?audio|dual)\b`)

var codecs = table(
	`\bav1\b`, "AV1",
	`x\s*265|h\.?\s*265|hevc`, "HEVC",
	`x\s*264|h\.?\s*264|\bavc\b`, "H264",
	`\bmpeg-?2\b`, "MPEG2",
	`\bvp9\b`, "VP9",
)

// Channel counts are often glued on ("AAC2.0"), so the trailing boundary allows digits.
var audioCodecs = table(
	`\be-?ac-?3\b|\bddp\b|dolby\s*digital\s*plus`, "EAC3",
	`\bac-?3\b`, "AC3",
	`\btruehd\b`, "TrueHD",
	`\bdts(?:-hd)?\b`, "DTS",
	`\bflac(?:\d(?:\.\d)?)?\b`, "FLAC",
	`\bopus(?:\d(?:\.\d)?)?\b`, "Opus",
	`\baac(?:\d(?:\.\d)?)?\b`, "AAC",
)

func Parse(name string) Release {
	r := Release{Raw: name}
	work := strings.TrimSpace(name)

	if m := extRe.FindStringSubmatch(work); m != nil {
		r.Extension = strings.ToLower(m[1])
		work = extRe.ReplaceAllString(work, "")
	}
	// "[CBM]_Monster_-_02_-_Downfall": underscores as spaces.
	if !strings.Contains(work, " ") && strings.Count(work, "_") >= 2 {
		work = strings.ReplaceAll(work, "_", " ")
	}

	r.Group = detectGroup(work)

	for _, tok := range bracketed.FindAllStringSubmatch(work, -1) {
		field := strings.TrimSpace(tok[1])
		if crc32Re.MatchString(field) && !isResolution(field) {
			r.CRC32 = strings.ToUpper(field)
		}
	}

	lower := strings.ToLower(work)
	r.Source = firstMatch(lower, sources)
	r.Codec = firstMatch(lower, codecs)
	r.DualAudio = dualAudio.MatchString(lower)
	r.Batch = isBatch(lower)
	// Against the original name: title cleaning removes the language bracket.
	r.Subtitles, r.HardSub = detectSubtitles(name)

	for _, a := range audioCodecs {
		if a.re.MatchString(lower) {
			r.Audio = append(r.Audio, a.name)
		}
	}

	if m := resolution.FindStringSubmatch(work); m != nil {
		switch {
		case m[1] != "":
			r.Resolution = m[1] + "p"
		case m[3] != "":
			r.Resolution = m[3] + "p"
		}
	}

	if m := bitDepthRe.FindStringSubmatch(lower); m != nil {
		switch {
		case m[2] != "":
			// Hi10P is H.264 High 10 Profile, so it implies the codec.
			r.BitDepth = 10
			if r.Codec == "" {
				r.Codec = "H264"
			}
		case m[1] != "":
			r.BitDepth, _ = strconv.Atoi(m[1])
		}
	}

	// The part is removed before numbering: "Part 2 - 17" otherwise reads as an episode range of 2 to 17.
	r.Part = partNumber(work)
	numbered := work
	if r.Part > 0 {
		numbered = partOnly.ReplaceAllString(work, " ")
	}
	r.Season, r.Episode, r.EpisodeEnd, r.Version = numbering(numbered, r.Batch)

	// A season with no episode is a pack; they rarely say "batch".
	if r.Season > 0 && r.Episode == 0 {
		r.Batch = true
	}
	// So is a stated span, tagged or not.
	if r.EpisodeEnd > r.Episode {
		r.Batch = true
	}

	r.Title = title(work, r)
	return r
}

// Group appears as a leading bracket, a trailing -NAME (scene), or the last
// standalone bracket; a space rules a bracket out, since tags like "Dual Audio" have one.
func detectGroup(work string) string {
	if m := leadGroup.FindStringSubmatch(work); m != nil {
		return strings.TrimSpace(m[1])
	}

	stripped := strings.TrimSpace(bracketed.ReplaceAllString(work, " "))
	if m := sceneGroup.FindStringSubmatch(stripped); m != nil {
		return m[1]
	}

	tokens := bracketed.FindAllStringSubmatch(work, -1)
	for _, token := range slices.Backward(tokens) {
		field := strings.TrimSpace(token[1])
		if field == "" || len(field) > 20 || strings.ContainsAny(field, " .") {
			continue
		}
		if crc32Re.MatchString(field) || isResolution(field) || metaStart.MatchString(" "+field) {
			continue
		}
		return field
	}
	return ""
}

// Unbracketed, 1920x1080 can pass for a checksum, so anything parsing as a resolution is excluded.
func isResolution(s string) bool {
	return resolution.MatchString(s)
}

// Scene releases use dots or underscores where fansubs use spaces. Comparing
// the two counts avoids mangling a title that legitimately contains a dot.
func isDotSeparated(s string) bool {
	return strings.Count(s, ".")+strings.Count(s, "_") > strings.Count(s, " ")
}

func firstMatch(lower string, table []named) string {
	for _, e := range table {
		if e.re.MatchString(lower) {
			return e.name
		}
	}
	return ""
}

func isBatch(lower string) bool {
	for _, marker := range []string{"[batch]", "(batch)", "batch", "complete", "bd box", "season 1-", "s1-"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

var romanParts = map[string]int{"I": 1, "II": 2, "III": 3, "IV": 4}

// PartOf reads a catalogue title's cour as a release name's is read, so the two compare.
func PartOf(title string) int { return partNumber(title) }

// SeasonOf reads the season out of a show's own title — "Frieren 2nd Season"
// is 2 — so a request can say which season it means without being told.
func SeasonOf(title string) int {
	season, _, _, _ := numbering(title, false)
	return season
}

var (
	trailingNumeral = regexp.MustCompile(`(?i)\s+(II|III|IV|V|VI|VII|VIII|IX|X|[2-9])[!?.]*$`)
	romanSeasons    = map[string]int{"II": 2, "III": 3, "IV": 4, "V": 5, "VI": 6, "VII": 7, "VIII": 8, "IX": 9, "X": 10}
)

// NumeralSeason reads a sequel's number before any subtitle ("Overlord IV", "… wo! 3"); roman says which kind.
func NumeralSeason(title string) (season int, roman bool) {
	for _, sep := range []string{":", " - "} {
		if i := strings.Index(title, sep); i > 0 {
			title = title[:i]
		}
	}
	m := trailingNumeral.FindStringSubmatch(strings.TrimSpace(title))
	if m == nil {
		return 0, false
	}
	if n, ok := romanSeasons[strings.ToUpper(m[1])]; ok {
		return n, true
	}
	n, _ := strconv.Atoi(m[1])
	return n, false
}

func partNumber(work string) int {
	m := partOnly.FindStringSubmatch(work)
	if m == nil {
		return 0
	}
	if n, err := strconv.Atoi(m[1]); err == nil {
		return n
	}
	return romanParts[strings.ToUpper(m[1])]
}

func numbering(work string, batch bool) (season, episode, end, version int) {
	if m := seasonEpSpan.FindStringSubmatch(work); m != nil {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		end, _ = strconv.Atoi(m[3])
		if end > episode {
			return season, episode, end, 0
		}
	}
	if m := seasonEp.FindStringSubmatch(work); m != nil {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		return season, episode, 0, 0
	}

	if m := seasonOnly.FindStringSubmatch(work); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				season, _ = strconv.Atoi(g)
				break
			}
		}
	}

	// A range means a batch, but a spaced dash separates the episode: "Steins;Gate 0 - 03" is episode 3.
	if m := rangeEp.FindStringSubmatchIndex(work); m != nil {
		lo, _ := strconv.Atoi(work[m[2]:m[3]])
		hi, _ := strconv.Atoi(work[m[4]:m[5]])
		span := work[m[0]:m[1]]
		separated := strings.Contains(span, " - ") || strings.Contains(span, " ~ ")
		// A spaced range opens after a separator or bracket, never a word: "Season 2 - 05".
		lead := strings.TrimRight(work[:m[2]], " ")
		opens := lead == "" || strings.ContainsAny(lead[len(lead)-1:], "-~([")
		if hi > lo && (batch || !separated || opens) {
			return season, lo, hi, 0
		}
	}
	if m := listedEp.FindStringSubmatch(work); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		if hi > lo {
			return season, lo, hi, 0
		}
	}

	m := dashEp.FindStringSubmatch(work)
	if m == nil {
		m = markedEp.FindStringSubmatch(work)
	}
	if m == nil {
		if b := bareEpisode(work); b != nil {
			m = []string{work[b[0]:b[1]], work[b[2]:b[3]], ""}
			if b[4] >= 0 {
				m[2] = work[b[4]:b[5]]
			}
		}
	}
	if m == nil {
		m = bracketEpisode(work)
	}
	if m != nil {
		episode, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			version, _ = strconv.Atoi(m[2])
		}
	}
	return season, episode, end, version
}

// bareEpisode locates the number in "Show 08 1080p", or nil. The separator must
// not belong to a number of its own, or "Evangelion.3.33.1080p" is episode 33.
func bareEpisode(work string) []int {
	for _, m := range bareEp.FindAllStringSubmatchIndex(work, -1) {
		// Either form of the number: [match, number, version] as six indexes.
		if m[2] < 0 {
			m = []int{m[0], m[1], m[6], m[7], m[8], m[9]}
		} else {
			m = m[:6]
		}
		if !hasWordBefore(work, m[2]) {
			continue
		}
		if m[0] > 0 && work[m[0]-1] >= '0' && work[m[0]-1] <= '9' {
			continue
		}
		// The 264 of "H.264".
		if m[0] > 0 && work[m[0]] == '.' && strings.ContainsRune("hHxX", rune(work[m[0]-1])) {
			continue
		}
		return m
	}
	return nil
}

// bracketEpisode reads "[05]" as the episode, skipping a year and a number that is the whole name.
func bracketEpisode(work string) []string {
	for _, m := range bracketEp.FindAllStringSubmatch(work, -1) {
		if n, _ := strconv.Atoi(m[1]); len(m[1]) == 4 && n >= 1900 && n <= 2100 {
			continue
		}
		return m
	}
	return nil
}

// hasWordBefore: a letter before the index, ignoring the group bracket.
func hasWordBefore(work string, idx int) bool {
	head := work[:idx]
	if m := leadGroup.FindString(head); m != "" {
		head = head[len(m):]
	}
	return strings.ContainsFunc(head, unicode.IsLetter)
}

func title(work string, r Release) string {
	// Before the brackets go, keeping the quality token the cut below needs.
	if b := bareEpisode(work); r.Episode > 0 && b != nil {
		end := b[3]
		if b[4] >= 0 {
			end = b[5]
		}
		work = work[:b[0]] + work[end:]
	}
	s := bracketed.ReplaceAllString(work, " ")

	if r.Group != "" {
		s = strings.TrimSuffix(strings.TrimSpace(s), "-"+r.Group)
	}
	// Underscores always separate; dots only when they outnumber spaces, as titles can hold one.
	s = strings.ReplaceAll(s, "_", " ")
	if isDotSeparated(s) {
		s = dotSeparated.ReplaceAllString(s, " ")
	}
	if r.Episode > 0 || r.Batch {
		s = seasonEpSpan.ReplaceAllString(s, " ")
		// "Show - 02 - Downfall": the title stops at the episode; what follows names the episode, not the show.
		if r.Episode > 0 && r.EpisodeEnd == 0 {
			s = beforeEpisode(s)
		}
		s = seasonEp.ReplaceAllString(s, " ")
		// Only a span the numbering accepted, or "Steins;Gate 0" loses its 0.
		if r.EpisodeEnd > 0 {
			s = rangeEp.ReplaceAllString(s, " ")
			s = listedEp.ReplaceAllString(s, " ")
		}
		s = endAfterEp.ReplaceAllString(s, "$1 ")
		s = dashEp.ReplaceAllString(s, " ")
		s = markedEp.ReplaceAllString(s, " ")
	}
	if r.Season > 0 {
		s = seasonOnly.ReplaceAllString(s, " ")
	}

	// Scene names have no brackets, so the quality run alone ends the title.
	if loc := metaStart.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}

	out := strings.Trim(strings.Join(strings.Fields(s), " "), " -_~,")
	if out == "" {
		out = bracketedTitle(work, r)
	}
	return out
}

// altName separates two names for the same show: "English … | Romaji".
var altName = regexp.MustCompile(`\s+[|/]\s+`)

// beforeEpisode cuts the name at its episode marker when a name precedes it, keeping an alternative name after.
func beforeEpisode(s string) string {
	for _, re := range []*regexp.Regexp{seasonEp, dashEp, markedEp} {
		loc := re.FindStringIndex(s)
		if loc == nil {
			continue
		}
		if !strings.ContainsFunc(s[:loc[0]], unicode.IsLetter) {
			return s
		}
		if alt := altName.FindStringIndex(s[loc[1]:]); alt != nil {
			return s[:loc[1]] + s[loc[1]+alt[0]:]
		}
		return s[:loc[1]]
	}
	return s
}

// bracketedTitle reads "[Group][Title][05][1080p]": the first bracket not a group, number, checksum or tag.
func bracketedTitle(work string, r Release) string {
	for i, tok := range bracketed.FindAllStringSubmatch(work, -1) {
		field := strings.TrimSpace(tok[1])
		if i == 0 && field == r.Group || !strings.ContainsFunc(field, unicode.IsLetter) {
			continue
		}
		if crc32Re.MatchString(field) || isResolution(field) || metaStart.MatchString(" "+field) {
			continue
		}
		if langs, _ := detectSubtitles("[" + field + "]"); len(langs) > 0 {
			continue
		}
		return field
	}
	return ""
}
