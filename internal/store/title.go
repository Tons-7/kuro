package store

import "context"

// Romaji is AniList's Latin transliteration (Sousou no Frieren, not 葬送のフリーレン).
// Native titles are stored but never chosen by this setting.
const (
	TitleRomaji  = "romaji"
	TitleEnglish = "english"
)

// PickTitle resolves which title to show. Many anime have no English title, so
// english falls back to romaji rather than leaving a card blank.
func PickTitle(mode, romaji string, english *string) string {
	if mode == TitleEnglish && english != nil && *english != "" {
		return *english
	}
	if romaji != "" {
		return romaji
	}
	if english != nil {
		return *english
	}
	return ""
}

// TitleMode reads the display preference, per anime where one is set.
func (s *Store) TitleMode(ctx context.Context, animeID int) string {
	prefs, err := s.Prefs(ctx, animeID)
	if err != nil {
		return TitleEnglish
	}
	if mode := prefs.String("display.titles"); mode == TitleRomaji {
		return TitleRomaji
	}
	return TitleEnglish
}
