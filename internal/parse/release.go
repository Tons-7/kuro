// Package parse turns an anime release filename into structured fields.
//
// Release names follow fansub convention rather than any standard, so this is
// pattern matching against the shapes that actually occur on public trackers:
//
//	[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E].mkv
//	[Erai-raws] Show - 07 [1080p HIDIVE WEB-DL AVC AAC][ABCD1234]
//	Show - S01E01 (BD Remux 1080p AVC FLAC) [Dual Audio] [PMR].mkv
//	[SubsPlease] Show (01-12) [Batch]
package parse

import (
	"regexp"
	"strconv"
	"strings"
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
	// Subtitles are the languages the name claims, which is all there is to go
	// on before downloading. Empty means unlabelled, not absent.
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
	resolution = regexp.MustCompile(`(?i)\b(\d{3,4})[pi]\b|\b(\d{3,4})x(\d{3,4})\b`)
	seasonEp   = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*E(\d{1,4})\b`)
	dashEp     = regexp.MustCompile(`(?:^|\s)-\s*(\d{1,4})(?:v(\d))?(?:\s|$)`)
	rangeEp    = regexp.MustCompile(`(?:^|[\s\(\[])(\d{1,4})\s*[-~]\s*(\d{1,4})(?:[\s\)\]]|$)`)
	// "E06", "EP06", "Ep. 6": S01E06 without the season. The leading boundary
	// keeps it out of checksums and codec names.
	markedEp = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])E(?:P|PISODE)?\.?\s*(\d{1,4})(?:v(\d))?\b`)
	// The same form listed rather than ranged: "[E41 E42 E43]".
	listedEp = regexp.MustCompile(`(?i)\bE(\d{1,4})(?:[\s,]+E(\d{1,4}))+\b`)
	// "S02SP01-02" has no boundary after the season, so specials get their own form.
	seasonOnly = regexp.MustCompile(`(?i)\b(?:S(\d{1,2})|Season\s*(\d{1,2}))\b|\b(\d)(?:nd|rd|th)\s*Season\b|\bS(\d{1,2})SP\d{1,4}(?:\s*-\s*\d{1,4})?\b`)
	partOnly   = regexp.MustCompile(`(?i)\b(?:part|cour)\s*(\d{1,2}|IV|I{1,3})\b`)
	bitDepthRe = regexp.MustCompile(`(?i)\b(8|10)\s*-?\s*bits?\b|\bhi(10)p\b`)
	extRe      = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|ts|m2ts|webm)$`)

	// Where the title stops and quality metadata begins, for scene-style names
	// with no brackets to strip.
	metaStart = regexp.MustCompile(`(?i)[\s._]+\b(\d{3,4}[pi]|BD|BDRip|BDMV|Blu-?ray|Remux|WEB-?DL|WEB-?Rip|WEB|HDTV|TVRip|DVD|DVDRip|CR|HIDIVE|AMZN|NF|Baha|Funi|x26[45]|H\.?26[45]|HEVC|AVC|AV1|VP9|MPEG-?2|AAC|FLAC|Opus|E?-?AC-?3|DDP?|DTS|TrueHD|Dual|Multi|10-?bits?|8-?bits?|Hi10P|Complete|Batch)\b`)

	dotSeparated = regexp.MustCompile(`[._]`)
)

// Source is checked longest-first so "BD Remux" is not swallowed by "BD".
var sources = []struct{ pattern, name string }{
	{`bd\s*remux|blu-?ray\s*remux|remux`, "BDRemux"},
	{`\bbdrip\b|\bbd\b|blu-?ray|\bbdmv\b`, "BD"},
	{`\bdvdrip\b|\bdvd\b`, "DVD"},
	{`web-?dl|web-?rip|\bweb\b|\bcr\b|hidive|\bamzn\b|\bnf\b|\bbaha\b|funi`, "WEB"},
	{`\bhdtv\b|\btvrip\b|\bbs11\b|\bbs\d{1,2}\b|\btx\b`, "TV"},
}

var dualAudio = regexp.MustCompile(`(?i)\b(?:dual[\s-]?audio|multi[\s-]?audio|dual)\b`)

var codecs = []struct{ pattern, name string }{
	{`\bav1\b`, "AV1"},
	{`x\s*265|h\.?\s*265|hevc`, "HEVC"},
	{`x\s*264|h\.?\s*264|\bavc\b`, "H264"},
	{`\bmpeg-?2\b`, "MPEG2"},
	{`\bvp9\b`, "VP9"},
}

// Channel counts are commonly glued on ("AAC2.0", "EAC3 5.1"), so the trailing
// word boundary has to allow digits.
var audioCodecs = []struct{ pattern, name string }{
	{`\be-?ac-?3\b|\bddp\b|dolby\s*digital\s*plus`, "EAC3"},
	{`\bac-?3\b`, "AC3"},
	{`\btruehd\b`, "TrueHD"},
	{`\bdts(?:-hd)?\b`, "DTS"},
	{`\bflac(?:\d(?:\.\d)?)?\b`, "FLAC"},
	{`\bopus(?:\d(?:\.\d)?)?\b`, "Opus"},
	{`\baac(?:\d(?:\.\d)?)?\b`, "AAC"},
}

func Parse(name string) Release {
	r := Release{Raw: name}
	work := strings.TrimSpace(name)

	if m := extRe.FindStringSubmatch(work); m != nil {
		r.Extension = strings.ToLower(m[1])
		work = extRe.ReplaceAllString(work, "")
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
		if regexp.MustCompile(a.pattern).MatchString(lower) {
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

	// The part is removed before numbering: "Part 2 - 17" otherwise reads as
	// an episode range of 2 to 17.
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
	for i := len(tokens) - 1; i >= 0; i-- {
		field := strings.TrimSpace(tokens[i][1])
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

// The resolution token 1920x1080 can look like a checksum once brackets are
// stripped, so exclude anything that parses as a resolution.
func isResolution(s string) bool {
	return resolution.MatchString(s)
}

// Scene releases use dots or underscores where fansubs use spaces. Comparing
// the two counts avoids mangling a title that legitimately contains a dot.
func isDotSeparated(s string) bool {
	return strings.Count(s, ".")+strings.Count(s, "_") > strings.Count(s, " ")
}

func firstMatch(lower string, table []struct{ pattern, name string }) string {
	for _, e := range table {
		if regexp.MustCompile(e.pattern).MatchString(lower) {
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

// PartOf reads the cour out of a catalogue title, the same way it is read out
// of a release name, so the two can be compared.
func PartOf(title string) int { return partNumber(title) }

// SeasonOf reads the season out of a show's own title — "Frieren 2nd Season"
// is 2 — so a request can say which season it means without being told.
func SeasonOf(title string) int {
	season, _, _, _ := numbering(title, false)
	return season
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

	// A range only means a batch; "01-12" inside a single-episode name would
	// otherwise swallow the episode number. A spaced dash is the episode
	// separator: "Steins;Gate 0 - 03" is episode 3, "Show 00-12" a pack.
	if m := rangeEp.FindStringSubmatch(work); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		separated := strings.Contains(m[0], " - ") || strings.Contains(m[0], " ~ ")
		if hi > lo && (lo > 0 || batch || !separated) {
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
	if m != nil {
		episode, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			version, _ = strconv.Atoi(m[2])
		}
	}
	return season, episode, end, version
}

func title(work string, r Release) string {
	s := bracketed.ReplaceAllString(work, " ")

	if r.Group != "" {
		s = strings.TrimSuffix(strings.TrimSpace(s), "-"+r.Group)
	}
	// Underscores are always separators; dots only when they outnumber spaces,
	// since a title can legitimately contain one.
	s = strings.ReplaceAll(s, "_", " ")
	if isDotSeparated(s) {
		s = dotSeparated.ReplaceAllString(s, " ")
	}
	if r.Episode > 0 || r.Batch {
		s = seasonEp.ReplaceAllString(s, " ")
		// Only a span the numbering accepted, or "Steins;Gate 0" loses its 0.
		if r.EpisodeEnd > 0 {
			s = rangeEp.ReplaceAllString(s, " ")
			s = listedEp.ReplaceAllString(s, " ")
		}
		s = dashEp.ReplaceAllString(s, " ")
		s = markedEp.ReplaceAllString(s, " ")
	}
	if r.Season > 0 {
		s = seasonOnly.ReplaceAllString(s, " ")
	}

	// Scene names carry no brackets, so the quality run is the only marker
	// separating the title from everything after it.
	if loc := metaStart.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}

	return strings.Trim(strings.Join(strings.Fields(s), " "), " -_~,")
}
