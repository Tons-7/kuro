package store

// Anime on MyAnimeList but not AniList are keyed on the negated MAL id. AniList
// ids are always positive, so the sign alone says which catalogue a record is.
func MALOnly(animeID int) bool { return animeID < 0 }

// MALIDOf returns the MyAnimeList id a local id stands for, or 0 when the id is
// an AniList one.
func MALIDOf(animeID int) int {
	if animeID < 0 {
		return -animeID
	}
	return 0
}

// LocalIDForMAL is the inverse, for resolving a MAL-only record by its MAL id.
func LocalIDForMAL(malID int) int {
	if malID <= 0 {
		return 0
	}
	return -malID
}
