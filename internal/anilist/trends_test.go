package anilist

import "testing"

func media(id, popularity int) Media {
	return Media{ID: id, Popularity: &popularity}
}

func TestRankByGainOrdersByWhatWasAddedInTheWindow(t *testing.T) {
	pool := []Media{
		media(1, 500_000), // huge, barely moved
		media(2, 40_000),  // small, moved a lot
		media(3, 90_000),
	}
	baseline := map[int]int{1: 499_000, 2: 25_000, 3: 88_000}

	got := rankByGain(pool, baseline)

	want := []int{2, 3, 1}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d = %d, want %d; a big library is not a big week", i, got[i].ID, id)
		}
	}
}

// A title with no reading at the window start has no measurable gain, so it is
// dropped rather than credited with its whole count.
func TestRankByGainDropsUnmeasurableTitles(t *testing.T) {
	pool := []Media{
		media(1, 61_000), // announced mid-window, no baseline
		media(2, 470_000),
	}
	baseline := map[int]int{2: 467_000}

	got := rankByGain(pool, baseline)

	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("got %+v, want only the title with a baseline", ids(got))
	}
}

func TestRankByGainSkipsMediaWithoutPopularity(t *testing.T) {
	pool := []Media{{ID: 1}, media(2, 100)}
	got := rankByGain(pool, map[int]int{1: 10, 2: 50})

	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("got %v, want only the title carrying a count", ids(got))
	}
}

func ids(list []Media) []int {
	out := make([]int, len(list))
	for i, m := range list {
		out[i] = m.ID
	}
	return out
}
