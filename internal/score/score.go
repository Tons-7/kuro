// Package score ranks the torrents found for one episode and decides which is
// safe to start automatically. Each candidate carries the reasons behind its score.
package score

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"kuro/internal/film"
	"kuro/internal/indexer"
	"kuro/internal/parse"
)

// Tier is one rung of the quality ladder, written "1080p:BD:HEVC:10". Only the
// components present are required, so "1080p:WEB" ignores codec and depth.
type Tier struct {
	Resolution string
	Source     string
	Codec      string
	BitDepth   int
}

func ParseTier(s string) Tier {
	var t Tier
	for part := range strings.SplitSeq(s, ":") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
		case strings.EqualFold(part, "BD"), strings.EqualFold(part, "WEB"),
			strings.EqualFold(part, "BDRemux"), strings.EqualFold(part, "TV"),
			strings.EqualFold(part, "DVD"):
			t.Source = canonicalSource(part)
		case strings.EqualFold(part, "HEVC"), strings.EqualFold(part, "H264"),
			strings.EqualFold(part, "AV1"), strings.EqualFold(part, "VP9"):
			t.Codec = strings.ToUpper(part)
		case isDigits(part):
			if n, _ := strconv.Atoi(part); n == 8 || n == 10 {
				t.BitDepth = n
			}
		default:
			t.Resolution = strings.ToLower(part)
		}
	}
	return t
}

func (t Tier) matches(r parse.Release) bool {
	if t.Resolution != "" && !strings.EqualFold(t.Resolution, r.Resolution) {
		return false
	}
	if t.Source != "" && !strings.EqualFold(t.Source, r.Source) {
		return false
	}
	if t.Codec != "" && !strings.EqualFold(t.Codec, r.Codec) {
		return false
	}
	if t.BitDepth != 0 && t.BitDepth != r.BitDepth {
		return false
	}
	return true
}

type Preferences struct {
	Ladder       []Tier
	MaxAutoBytes int64
	AllowHi10P   bool
	PreferGroups []string
	// "sub", "dub" or "either".
	Audio string
	// Raws carry no subtitles, so they are refused unless asked for by name.
	AllowRaw bool
	// Best first; a release in none ranks down, never refused, as it may be the only one.
	SubLanguages []string
	// HardwareTranscode: a real-time H.264 encoder here. Without one, HEVC and Hi10P stall on seeks.
	HardwareTranscode bool
}

// needsSoftwareTranscode: HEVC or Hi10P. Unlabelled codecs are left alone, being mostly plain H.264.
func needsSoftwareTranscode(rel parse.Release) bool {
	switch rel.Codec {
	case "HEVC":
		return true
	case "H264":
		return rel.BitDepth == 10
	}
	return false
}

func DefaultPreferences() Preferences {
	return Preferences{
		Ladder: []Tier{
			ParseTier("1080p:BD:HEVC:10"),
			ParseTier("1080p:BD"),
			ParseTier("1080p:WEB"),
			ParseTier("720p"),
		},
		MaxAutoBytes: 3 << 30,
		Audio:        "sub",
		SubLanguages: []string{"en"},
	}
}

var (
	dubbed   = regexp.MustCompile(`(?i)\b(dub|dubbed|english[\s._-]?dub|eng[\s._-]?dub)\b`)
	subbed   = regexp.MustCompile(`(?i)\b(sub|subbed|softsub|hardsub|multi[\s._-]?subs?|eng[\s._-]?subs?)\b`)
	dualAudi = regexp.MustCompile(`(?i)\bdual[\s._-]?audio\b`)
)

// A dual-audio release satisfies either preference; the track is chosen at
// playback. Only a release that is exclusively the other one is rejected.
func audioMismatch(rel parse.Release, want string) bool {
	if want == "" || want == "either" {
		return false
	}
	raw := rel.Raw
	if rel.DualAudio || dualAudi.MatchString(raw) {
		return false
	}

	switch want {
	case "dub":
		return !dubbed.MatchString(raw)
	case "sub":
		return dubbed.MatchString(raw) && !subbed.MatchString(raw)
	}
	return false
}

type Candidate struct {
	Torrent indexer.Torrent
	Release parse.Release

	// SeaDex's curated best (by AniList id) outweighs any heuristic.
	SeaDexBest  bool
	SeaDexGroup bool

	// Episodes in the show, used to size a single episode inside a batch.
	TotalEpisodes int

	// One episode's length in minutes, 0 when unknown; scales the size limit.
	RuntimeMinutes int

	// The release states the episode asked for. Anything else is a guess, however well seeded.
	Confirmed bool

	// The release names some other show. Set by the finder, which knows the titles this one goes by.
	WrongShow bool

	// Numbers the episode's file may carry in this release, the asked-for one first.
	Numbers []int `json:"-"`
	// Episode is the one asked for, which Numbers may or may not carry.
	Episode int `json:"-"`

	// Film is set for one film of a film series: its file is found by name, not by an episode number.
	Film *film.Key `json:"-"`
	// The release names another film of the same series.
	WrongFilm bool
}

// The limit is set for an episode of this length; longer runtimes scale it.
const nominalRuntime = 24

// SizeLimit scales the cap by runtime: a 50-minute film is twice a 24-minute episode.
func (c Candidate) SizeLimit(prefs Preferences) int64 {
	if prefs.MaxAutoBytes <= 0 || c.RuntimeMinutes <= nominalRuntime {
		return prefs.MaxAutoBytes
	}
	scaled := float64(prefs.MaxAutoBytes) * float64(c.RuntimeMinutes) / nominalRuntime
	if limit := int64(scaled); limit > prefs.MaxAutoBytes {
		return limit
	}
	return prefs.MaxAutoBytes
}

// EpisodeBytes is what will actually be downloaded. Only the requested file is
// fetched from a pack, so a pack's total size overstates the cost for older shows.
func (c Candidate) EpisodeBytes() int64 {
	// A batch of a film series holds every film; one of them is its share.
	if c.Film != nil {
		if c.Release.Batch && c.Film.Count > 1 {
			return c.Torrent.Size / int64(c.Film.Count)
		}
		return c.Torrent.Size
	}
	if !c.Release.Batch && c.Release.Episode > 0 {
		return c.Torrent.Size
	}

	count := c.Release.EpisodeEnd - c.Release.Episode + 1
	if count < 2 {
		count = c.TotalEpisodes
	}
	if count < 2 {
		return c.Torrent.Size
	}
	return c.Torrent.Size / int64(count)
}

type Result struct {
	Candidate
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons"`
	// Playable is false only for a release this machine would software-transcode.
	Playable   bool        `json:"playable"`
	AutoPick   bool        `json:"autoPick"`
	Blocked    string      `json:"blocked,omitempty"`
	Rejections []Rejection `json:"rejections,omitempty"`
	// resRank is the resolution's place on the ladder, the first quality key.
	resRank float64
	// unreadable: subtitled only in languages the viewer does not read.
	unreadable bool
}

const (
	tierStep            = 100.0
	partialTier         = 0.6
	seaDexBestBonus     = 260.0
	seaDexGroup         = 90.0
	preferredGroup      = 70.0
	trustedBonus        = 25.0
	dualAudioBonus      = 5.0
	batchPenalty        = 15.0
	unknownScopePenalty = 25.0
	seedWeight          = 22.0
	// Under the smallest tier gap, so seeders order equals and never buy a lower resolution.
	maxSeedBonus = 50.0
	// A handful of seeders streams badly however good the release is.
	thinSwarm        = 5
	thinSwarmPenalty = 30.0

	// Small: a label only breaks ties; most English releases state none. Language itself is a key in better().
	subLanguageBonus  = 15.0
	wrongLanguage     = 170.0
	hardSubbedPenalty = 60.0
)

// Rank orders candidates best first, ineligible ones included for the manual picker.
func Rank(cands []Candidate, prefs Preferences) []Result {
	out := make([]Result, 0, len(cands))
	for _, c := range cands {
		out = append(out, evaluate(c, prefs))
	}

	// Insertion sort keeps ties in indexer order; the sets are small.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && better(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// A batch is a fallback: a single episode starts sooner, however well the pack is seeded.
func better(a, b Result) bool {
	if a.AutoPick != b.AutoPick {
		return a.AutoPick
	}
	// No resolution named is unproven quality: last, even if it is the only one naming the episode.
	if a.AutoPick && (a.Release.Resolution == "") != (b.Release.Resolution == "") {
		return b.Release.Resolution == ""
	}
	// A stated episode beats a maybe, so a numberless film can't outscore the episode.
	if a.Confirmed != b.Confirmed {
		return a.Confirmed
	}
	// An old film's single release is often one seeder; its series batch is the copy people keep.
	if a.AutoPick && a.Film != nil && b.Film != nil && thin(a) != thin(b) {
		return !thin(a)
	}
	if a.AutoPick && a.Release.Batch != b.Release.Batch {
		return !a.Release.Batch
	}
	// Strict keys, so arrival order never matters: SeaDex best, resolution, readable subs, playable, score.
	if a.AutoPick && a.SeaDexBest != b.SeaDexBest {
		return a.SeaDexBest
	}
	if a.AutoPick && a.resRank != b.resRank {
		return a.resRank > b.resRank
	}
	// Quality first; within a resolution, subtitles the viewer cannot read lose whatever else they have.
	if a.AutoPick && a.unreadable != b.unreadable {
		return !a.unreadable
	}
	if a.AutoPick && a.Playable != b.Playable {
		return a.Playable
	}
	return a.Score > b.Score
}

// thin: counted at a handful of seeders, which streams badly. An unknown count isn't thin.
func thin(r Result) bool { return r.Torrent.SeedersKnown && r.Torrent.Seeders < thinSwarm }

// Best returns the release to start automatically, or false for the user to choose.
func Best(cands []Candidate, prefs Preferences) (Result, bool) {
	ranked := Rank(cands, prefs)
	if len(ranked) == 0 || !ranked[0].AutoPick {
		return Result{}, false
	}
	return ranked[0], true
}

func evaluate(c Candidate, prefs Preferences) Result {
	r := Result{Candidate: c, AutoPick: true}
	rel := c.Release
	r.resRank = resolutionRank(rel.Resolution, prefs)

	r.Playable = prefs.HardwareTranscode || !needsSoftwareTranscode(rel)
	if !r.Playable {
		r.add("needs software transcoding on this machine; a seek may stall")
	}

	if r.Rejections = evaluateSpecs(c, prefs); len(r.Rejections) > 0 {
		r.Blocked = r.Rejections[0].Reason
		r.AutoPick = false
	}

	if !r.applyLadder(rel, prefs) {
		// Unlabelled source or codec matches no tier, but its known resolution beats a lower one.
		if rank := resolutionRank(rel.Resolution, prefs); rank > 0 {
			r.Score += rank * tierStep * partialTier
			r.addf("%s, unlabelled source", rel.Resolution)
		}
	}

	switch {
	case c.SeaDexBest:
		r.Score += seaDexBestBonus
		r.add("curated best release on SeaDex")
	case c.SeaDexGroup:
		r.Score += seaDexGroup
		r.add("group appears in SeaDex picks")
	}

	for _, g := range prefs.PreferGroups {
		if strings.EqualFold(g, rel.Group) {
			r.Score += preferredGroup
			r.addf("preferred group %s", rel.Group)
			break
		}
	}

	r.applySubtitles(rel, prefs)

	if c.Torrent.Trusted {
		r.Score += trustedBonus
		r.add("trusted uploader")
	}
	if rel.DualAudio {
		r.Score += dualAudioBonus
		r.add("dual audio, Japanese track selectable")
	}
	switch {
	case rel.Batch:
		r.Score -= batchPenalty
		r.add("batch, needs an extra step to find the episode")
	case rel.Episode == 0:
		// No episode or batch marker: probably a pack, confirmed by its file list.
		r.Score -= unknownScopePenalty
		r.add("episode not stated in the name")
	}

	// Log scaled and capped: a big swarm decides between equals only.
	switch {
	case !c.Torrent.SeedersKnown:
		r.add("swarm size unknown")
	case c.Torrent.Seeders > 0:
		r.Score += min(math.Log1p(float64(c.Torrent.Seeders))*seedWeight, maxSeedBonus)
		r.addf("%d seeders", c.Torrent.Seeders)
		if c.Torrent.Seeders < thinSwarm {
			r.Score -= thinSwarmPenalty
			r.add("few seeders, expect a slow download")
		}
	}

	if r.Score < 0 {
		r.Score = 0
	}
	return r
}

// applySubtitles ranks on named languages; unlabelled is left alone, as English releases rarely say so.
func (r *Result) applySubtitles(rel parse.Release, prefs Preferences) {
	if len(prefs.SubLanguages) == 0 || len(rel.Subtitles) == 0 {
		return
	}

	if parse.HasSubtitleLanguage(rel, prefs.SubLanguages) {
		r.Score += subLanguageBonus
		r.addf("subtitles in %s", strings.Join(rel.Subtitles, ", "))
		return
	}

	r.Score -= wrongLanguage
	r.unreadable = true
	r.addf("subtitles only in %s", strings.Join(rel.Subtitles, ", "))
	if rel.HardSub {
		// Burnt into the picture: there is no track to switch away from.
		r.Score -= hardSubbedPenalty
		r.add("hardsubbed, the track cannot be turned off")
	}
}

func (r *Result) applyLadder(rel parse.Release, prefs Preferences) bool {
	for i, tier := range prefs.Ladder {
		if tier.matches(rel) {
			r.Score += float64(len(prefs.Ladder)-i) * tierStep
			r.addf("matches quality tier %d", i+1)
			return true
		}
	}
	return false
}

// Ranks by the user's ladder, not pixels, so an unasked 2160p can't win; unlabelled sits under labelled.
func resolutionRank(res string, prefs Preferences) float64 {
	res = strings.ToLower(res)
	if res == "" {
		return 0
	}

	var seen int
	var rank float64
	for i, tier := range prefs.Ladder {
		if tier.Resolution == "" {
			continue
		}
		seen++
		if tier.Resolution == res {
			rank = float64(len(prefs.Ladder) - i)
		}
	}
	if rank > 0 {
		return rank
	}
	if seen == 0 {
		return 0
	}
	// Present but unwanted: worth something, worth less than any listed tier.
	return 0.5
}

func (r *Result) add(reason string) { r.Reasons = append(r.Reasons, reason) }

func (r *Result) addf(format string, args ...any) {
	r.Reasons = append(r.Reasons, fmt.Sprintf(format, args...))
}

func canonicalSource(s string) string {
	switch strings.ToLower(s) {
	case "bdremux":
		return "BDRemux"
	case "bd":
		return "BD"
	case "web":
		return "WEB"
	case "tv":
		return "TV"
	case "dvd":
		return "DVD"
	}
	return s
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
