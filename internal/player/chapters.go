package player

import (
	"fmt"
	"os"
	"strings"
)

// The tags matter: this crosses the wire to the browser player, which reads
// lower-case names like every other type in the API.
type SkipRange struct {
	Kind  string  `json:"kind"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func (s SkipRange) Contains(t float64) bool { return t >= s.Start && t < s.End }

// Chapter titles are matched by uosc's chapter_range_patterns to tint the
// seekbar, so these names are load-bearing, not decorative.
func chapterTitle(kind string) string {
	switch strings.ToLower(kind) {
	case "op", "mixed-op":
		return "Opening"
	case "ed", "mixed-ed":
		return "Ending"
	case "recap":
		return "Recap"
	default:
		return strings.ToUpper(kind)
	}
}

// WriteChapters emits an ffmetadata file for mpv's --chapters-file. Skip data
// lives outside the video, so chapters have to be supplied alongside it.
func WriteChapters(path string, ranges []SkipRange, duration float64) error {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")

	prev := 0.0
	for _, r := range ranges {
		if r.End <= r.Start {
			continue
		}
		if r.Start > prev {
			writeChapter(&b, prev, r.Start, "Episode")
		}
		writeChapter(&b, r.Start, r.End, chapterTitle(r.Kind))
		prev = r.End
	}
	if duration > prev {
		writeChapter(&b, prev, duration, "Episode")
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeChapter(b *strings.Builder, start, end float64, title string) {
	fmt.Fprintf(b, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n",
		int64(start*1000), int64(end*1000), title)
}
