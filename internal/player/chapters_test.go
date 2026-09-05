package player

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteChaptersFillsGaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chapters.ini")
	err := WriteChapters(path, []SkipRange{
		{Kind: "op", Start: 128, End: 218},
		{Kind: "ed", Start: 1343, End: 1433},
	}, 1440)
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	if !strings.HasPrefix(got, ";FFMETADATA1") {
		t.Fatalf("missing ffmetadata header:\n%s", got)
	}
	// uosc tints the seekbar by matching these titles.
	for _, want := range []string{"title=Opening", "title=Ending", "title=Episode"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(got, "[CHAPTER]"); n != 5 {
		t.Errorf("%d chapters, want 5 (pre-OP, OP, body, ED, tail)", n)
	}
	if !strings.Contains(got, "START=128000") || !strings.Contains(got, "END=218000") {
		t.Errorf("opening not written in milliseconds:\n%s", got)
	}
}

func TestWriteChaptersEdgeCases(t *testing.T) {
	dir := t.TempDir()

	t.Run("no ranges", func(t *testing.T) {
		path := filepath.Join(dir, "a.ini")
		if err := WriteChapters(path, nil, 1440); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(path)
		if n := strings.Count(string(body), "[CHAPTER]"); n != 1 {
			t.Errorf("%d chapters, want a single episode chapter", n)
		}
	})

	t.Run("zero length range is skipped", func(t *testing.T) {
		path := filepath.Join(dir, "b.ini")
		WriteChapters(path, []SkipRange{{Kind: "op", Start: 100, End: 100}}, 1440)
		body, _ := os.ReadFile(path)
		if strings.Contains(string(body), "title=Opening") {
			t.Error("wrote a zero-length chapter")
		}
	})

	t.Run("opening at zero has no gap chapter", func(t *testing.T) {
		path := filepath.Join(dir, "c.ini")
		WriteChapters(path, []SkipRange{{Kind: "op", Start: 0, End: 90}}, 1440)
		body, _ := os.ReadFile(path)
		if n := strings.Count(string(body), "[CHAPTER]"); n != 2 {
			t.Errorf("%d chapters, want opening plus body", n)
		}
	})
}

func TestChapterTitle(t *testing.T) {
	tests := map[string]string{
		"op": "Opening", "mixed-op": "Opening",
		"ed": "Ending", "mixed-ed": "Ending",
		"recap": "Recap",
	}
	for kind, want := range tests {
		if got := chapterTitle(kind); got != want {
			t.Errorf("chapterTitle(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestSkipRangeContains(t *testing.T) {
	r := SkipRange{Start: 128, End: 218}

	cases := map[float64]bool{127.9: false, 128: true, 200: true, 217.9: true, 218: false, 300: false}
	for at, want := range cases {
		if got := r.Contains(at); got != want {
			t.Errorf("Contains(%.1f) = %v, want %v", at, got, want)
		}
	}
}
