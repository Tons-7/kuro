package library

import (
	"encoding/json"

	"kuro/internal/score"
	"kuro/internal/store"
)

type scorePrefs = score.Preferences

func defaultScorePrefs() scorePrefs { return score.DefaultPreferences() }

// prefsFromSettings turns resolved settings into scoring preferences. Settings
// are pre-resolved, so a per-show override is applied like a global default.
func prefsFromSettings(p store.Prefs) scorePrefs {
	prefs := score.DefaultPreferences()

	var tiers []string
	if err := json.Unmarshal([]byte(p.String("quality.ladder")), &tiers); err == nil && len(tiers) > 0 {
		prefs.Ladder = make([]score.Tier, 0, len(tiers))
		for _, t := range tiers {
			prefs.Ladder = append(prefs.Ladder, score.ParseTier(t))
		}
	}
	if n := p.Int64("quality.max_auto_bytes"); n > 0 {
		prefs.MaxAutoBytes = n
	}
	prefs.AllowHi10P = p.Bool("quality.allow_hi10p")

	if g := p.String("release.prefer_groups"); g != "" {
		var groups []string
		if json.Unmarshal([]byte(g), &groups) == nil {
			prefs.PreferGroups = groups
		}
	}
	if a := p.String("audio.prefer"); a != "" {
		prefs.Audio = a
	}
	if l := p.String("subtitle.languages"); l != "" {
		var langs []string
		if json.Unmarshal([]byte(l), &langs) == nil {
			prefs.SubLanguages = langs
		}
	}
	// Never for an unattended download: the point of waiting is that a subbed
	// release turns up a few hours after the raw.
	prefs.AllowRaw = false
	return prefs
}

// Preferences is the entry point for callers that have an anime id.
func Preferences(p store.Prefs) score.Preferences { return prefsFromSettings(p) }
