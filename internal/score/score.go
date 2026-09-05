// Package score ranks the torrents found for one episode and decides which is
// safe to start automatically. Each candidate carries the reasons behind its score.
package score

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

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
	for _, part := range strings.Split(s, ":") {
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
	// Best first. A release in none is ranked down, never refused — for some
	// shows it's the only one that exists.
	SubLanguages []string
	// HardwareTranscode is set when this machine has a real-time hardware
	// H.264 encoder. Without it, HEVC and 10-bit H.264 must be software
	// transcoded, which stalls on seeks, so a directly-playable release of the
	// same resolution is preferred (a browser plays H.264 8-bit untouched).
	HardwareTranscode bool
}

// needsSoftwareTranscode reports a codec a browser cannot decode and this build
// re-encodes: HEVC always, 10-bit H.264 (Hi10P) which has no hardware decode.
// Unknown/VP9/AV1 are left alone — either they play or they parse as unlabelled
// H.264, and penalising those would drop the common well-seeded release.
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

	// SeaDex curates the best release per anime, keyed by AniList id, so a
	// match there outweighs any heuristic.
	SeaDexBest  bool
	SeaDexGroup bool

	// Episodes in the show, used to size a single episode inside a batch.
	TotalEpisodes int

	// The release states the episode asked for. Anything else is a guess,
	// however well seeded.
	Confirmed bool

	// The release names some other show. Set by the finder, which knows the
	// titles this one goes by.
	WrongShow bool

	// Numbers a file for the episode may carry inside this release, the one
	// asked for first. A release that names no cour counts from the first one,
	// so for a later cour only the numbers carried across cours are listed.
	Numbers []int `json:"-"`
}

// EpisodeBytes is what will actually be downloaded. Only the requested file is
// fetched from a pack, so a pack's total size overstates the cost for older shows.
func (c Candidate) EpisodeBytes() int64 {
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
	// Playable is false only for a release this machine would software-transcode
	// (HEVC or Hi10P without a hardware encoder). Among one resolution the
	// playable release wins, so playback does not stall on a seek.
	Playable   bool        `json:"playable"`
	AutoPick   bool        `json:"autoPick"`
	Blocked    string      `json:"blocked,omitempty"`
	Rejections []Rejection `json:"rejections,omitempty"`
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
	seedWeight          = 9.0

	// Larger than a tier step, so the quality ladder cannot overturn language:
	// an unreadable 1080p is worth less than a readable 720p.
	subLanguageBonus  = 130.0
	wrongLanguage     = 170.0
	hardSubbedPenalty = 60.0
)

// Rank orders candidates best first. Everything is returned, including
// ineligible releases, so the manual picker can still show them.
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

// A batch is a fallback, not a preference: a single episode starts sooner and
// needs no file selection, however well the pack is seeded.
func better(a, b Result) bool {
	if a.AutoPick != b.AutoPick {
		return a.AutoPick
	}
	// A stated episode beats a maybe outright, so a film with no episode number
	// can't outrank the actual episode on score alone.
	if a.Confirmed != b.Confirmed {
		return a.Confirmed
	}
	if a.AutoPick && a.Release.Batch != b.Release.Batch {
		return !a.Release.Batch
	}
	// Within one resolution, a release that plays as-is beats one this machine
	// would software-transcode (which stalls on seeks). Across resolutions the
	// quality score still decides, so 1080p HEVC still beats 720p H.264.
	if a.AutoPick && a.Release.Resolution == b.Release.Resolution && a.Playable != b.Playable {
		return a.Playable
	}
	return a.Score > b.Score
}

// Best returns the release to start automatically, or false when nothing
// qualifies and the user should choose.
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

	r.Playable = prefs.HardwareTranscode || !needsSoftwareTranscode(rel)
	if !r.Playable {
		r.add("needs software transcoding on this machine; a seek may stall")
	}

	if r.Rejections = evaluateSpecs(c, prefs); len(r.Rejections) > 0 {
		r.Blocked = r.Rejections[0].Reason
		r.AutoPick = false
	}

	if !r.applyLadder(rel, prefs) {
		// An unlabelled source or codec matches no tier, but the resolution is
		// known and must not lose to a lower one.
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
		// Neither an episode number nor a batch marker: probably a season
		// pack, confirmed only once the file list is known.
		r.Score -= unknownScopePenalty
		r.add("episode not stated in the name")
	}

	// Log scaled: 1000 seeders must not drown out quality, but 0 against 20
	// decides how long the first frame takes.
	switch {
	case !c.Torrent.SeedersKnown:
		r.add("swarm size unknown")
	case c.Torrent.Seeders > 0:
		r.Score += math.Log1p(float64(c.Torrent.Seeders)) * seedWeight
		r.addf("%d seeders", c.Torrent.Seeders)
	}

	if r.Score < 0 {
		r.Score = 0
	}
	return r
}

// applySubtitles ranks on the languages a release names. An unlabelled release
// is left alone: most English releases never say "English".
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

// Ranks by the user's ladder, not absolute pixel count, so a resolution they
// never asked for (2160p is usually an upscale) can't outrank one they did.
func resolutionRank(res string, prefs Preferences) float64 {
	res = strings.ToLower(res)
	if res == "" {
		return 0
	}

	var seen int
	for i, tier := range prefs.Ladder {
		if tier.Resolution == "" {
			continue
		}
		seen++
		if tier.Resolution == res {
			return float64(len(prefs.Ladder) - i)
		}
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
