package parse

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// A corpus of real release names from a public tracker: curated cases prove the
// parser handles what it was written for, this proves it survives what it wasn't.
func corpus(t *testing.T) []string {
	t.Helper()

	f, err := os.Open("testdata/nyaa_titles.txt")
	if err != nil {
		t.Skipf("corpus unavailable: %v", err)
	}
	defer f.Close()

	var names []string
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		if line := strings.TrimSpace(scan.Text()); line != "" {
			names = append(names, line)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func TestCorpusExtractionRates(t *testing.T) {
	names := corpus(t)
	if len(names) < 100 {
		t.Fatalf("corpus has only %d entries", len(names))
	}

	var group, resolution, numbered, titled int
	for _, name := range names {
		r := Parse(name)
		if r.Group != "" {
			group++
		}
		if r.Resolution != "" {
			resolution++
		}
		if r.Episode > 0 || r.Batch {
			numbered++
		}
		if r.Title != "" {
			titled++
		}
	}

	total := float64(len(names))
	checks := []struct {
		name string
		got  int
		min  float64
	}{
		{"group", group, 0.90},
		{"resolution", resolution, 0.75},
		{"episode or batch", numbered, 0.80},
		{"title", titled, 0.98},
	}

	for _, c := range checks {
		rate := float64(c.got) / total
		t.Logf("%-18s %3d/%d  %.1f%%", c.name, c.got, len(names), rate*100)
		if rate < c.min {
			t.Errorf("%s extracted for %.1f%%, want at least %.0f%%", c.name, rate*100, c.min*100)
		}
	}
}

// The title is what gets matched against AniList, so leaking quality tags into
// it breaks episode lookup.
func TestCorpusTitlesAreClean(t *testing.T) {
	leaks := []string{
		"1080p", "720p", "480p", "2160p", "x265", "x264", "HEVC", "AVC",
		"WEB-DL", "WEBRip", "BDRip", "Blu-ray", "FLAC", "Opus", "10bit",
		"Dual Audio", "Multi-Subs", "Batch",
	}

	var bad int
	for _, name := range corpus(t) {
		title := strings.ToLower(Parse(name).Title)
		for _, leak := range leaks {
			if strings.Contains(title, strings.ToLower(leak)) {
				if bad < 10 {
					t.Errorf("%q leaked into title %q", leak, title)
				}
				bad++
				break
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d titles carry quality tags", bad, len(corpus(t)))
	}
}

func TestCorpusNeverPanics(t *testing.T) {
	for _, name := range corpus(t) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on %q: %v", name, r)
				}
			}()
			Parse(name)
		}()
	}
}
