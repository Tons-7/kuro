package score

import (
	"fmt"
	"regexp"
	"strings"
)

// Rejection names the rule that refused a release, so the reason survives to
// the UI instead of collapsing into one opaque string.
type Rejection struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// Independent specs decide acceptance (à la Sonarr), separate from scoring; the
// first tier to reject stops the rest, so a definitive rule isn't hidden behind a guess.
type spec struct {
	name   string
	tier   int
	reject func(Candidate, Preferences) string
}

const (
	tierUnplayable = 1
	tierWrongThing = 2
	tierHardware   = 3
	tierPolicy     = 4
)

var (
	discImage  = regexp.MustCompile(`(?i)\b(bdmv|dvdiso|bdiso|iso|disc\s*\d*|avc\s+dts-hd)\b`)
	sampleFile = regexp.MustCompile(`(?i)\bsample\b`)
)

var specs = []spec{
	{
		name: "wrong show", tier: tierWrongThing,
		reject: func(c Candidate, _ Preferences) string {
			if c.WrongShow {
				return "names a different show"
			}
			return ""
		},
	},
	{
		name: "seeders", tier: tierUnplayable,
		reject: func(c Candidate, _ Preferences) string {
			// Only a counted zero is a dead swarm.
			if c.Torrent.SeedersKnown && c.Torrent.Seeders == 0 {
				return "no seeders"
			}
			return ""
		},
	},
	{
		// A disc image is a whole Blu-ray, not an episode: hours of menus and
		// VOB structure that no player here can seek into.
		name: "disc image", tier: tierUnplayable,
		reject: func(c Candidate, _ Preferences) string {
			if discImage.MatchString(c.Torrent.Title) {
				return "disc image rather than an episode file"
			}
			return ""
		},
	},
	{
		name: "sample", tier: tierUnplayable,
		reject: func(c Candidate, _ Preferences) string {
			if sampleFile.MatchString(c.Torrent.Title) {
				return "sample clip"
			}
			return ""
		},
	},
	{
		name: "raw", tier: tierWrongThing,
		reject: func(c Candidate, prefs Preferences) string {
			if prefs.AllowRaw {
				return ""
			}
			if IsRaw(c) {
				return "raw capture, no subtitles"
			}
			return ""
		},
	},
	{
		name: "audio", tier: tierWrongThing,
		reject: func(c Candidate, prefs Preferences) string {
			if !audioMismatch(c.Release, prefs.Audio) {
				return ""
			}
			if prefs.Audio == "dub" {
				return "no English dub"
			}
			return "English dub only"
		},
	},
	{
		name: "hi10p", tier: tierHardware,
		reject: func(c Candidate, prefs Preferences) string {
			if !prefs.AllowHi10P && c.Release.Codec == "H264" && c.Release.BitDepth == 10 {
				return "10-bit H.264 has no hardware decode"
			}
			return ""
		},
	},
	{
		name: "size", tier: tierPolicy,
		reject: func(c Candidate, prefs Preferences) string {
			if prefs.MaxAutoBytes > 0 && c.EpisodeBytes() > prefs.MaxAutoBytes {
				return "larger than the automatic size limit"
			}
			return ""
		},
	},
}

// evaluateSpecs returns every rejection from the first tier that refuses the
// release, so the message is about the real problem, not its consequences.
func evaluateSpecs(c Candidate, prefs Preferences) []Rejection {
	var out []Rejection
	tier := 0

	for _, s := range specs {
		if len(out) > 0 && s.tier != tier {
			break
		}
		reason := s.reject(c, prefs)
		if reason == "" {
			continue
		}
		if len(out) == 0 {
			tier = s.tier
		}
		out = append(out, Rejection{Rule: s.name, Reason: reason})
	}
	return out
}

func (r Rejection) String() string { return fmt.Sprintf("%s: %s", r.Rule, r.Reason) }

// IsRaw reports an untranslated broadcast capture. Transport streams are
// always raws, and trackers file them under a raw category.
func IsRaw(c Candidate) bool {
	return c.Release.Extension == "ts" ||
		strings.Contains(strings.ToLower(c.Torrent.Category), "raw")
}

// RejectedOnlyAsRaw reports a release that would have been picked if it
// carried subtitles, which is the normal state of a brand-new episode.
func (r Result) RejectedOnlyAsRaw() bool {
	if len(r.Rejections) == 0 {
		return false
	}
	for _, rejection := range r.Rejections {
		if rejection.Rule != "raw" {
			return false
		}
	}
	return true
}
