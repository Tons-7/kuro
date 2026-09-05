package match

// Sørensen-Dice over character bigrams. Titles differ by transliteration, not
// typos, so bigram overlap beats edit distance here.
func dice(a, b string) float64 {
	if a == b {
		return 1
	}
	ar, br := []rune(a), []rune(b)
	if len(ar) < 2 || len(br) < 2 {
		return 0
	}

	counts := make(map[uint64]int, len(ar))
	for i := 0; i < len(ar)-1; i++ {
		counts[bigram(ar[i], ar[i+1])]++
	}

	var shared int
	for i := 0; i < len(br)-1; i++ {
		k := bigram(br[i], br[i+1])
		if counts[k] > 0 {
			counts[k]--
			shared++
		}
	}

	total := (len(ar) - 1) + (len(br) - 1)
	return 2 * float64(shared) / float64(total)
}

func bigram(a, b rune) uint64 {
	return uint64(uint32(a))<<32 | uint64(uint32(b))
}
