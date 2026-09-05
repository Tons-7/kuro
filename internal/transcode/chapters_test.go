package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestChapterKind(t *testing.T) {
	// The vocabulary release groups actually use.
	for title, want := range map[string]string{
		"Intro": "op", "Opening": "op", "OP": "op", "op": "op",
		"Opening Theme": "op", "OP - Kick Back": "op",
		"Credits": "ed", "Ending": "ed", "ED": "ed", "Outro": "ed",
		"Ending Theme": "ed", "End Credits": "ed",
		// Release groups number them.
		"OP1": "op", "OP 1": "op", "OP-01": "op", "ED1": "ed", "ED 2": "ed", "ED-01": "ed",
		"Scene 1": "", "Part B": "", "Preview": "", "": "", "Stop": "", "Prologue": "",
	} {
		if got := chapterKind(title); got != want {
			t.Errorf("chapterKind(%q) = %q, want %q", title, got, want)
		}
	}
}

// The shape a real release ships: named chapters around the episode body.
func TestProbeReadsChapters(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	dir := t.TempDir()

	meta := filepath.Join(dir, "chapters.ini")
	if err := os.WriteFile(meta, []byte(`;FFMETADATA1
[CHAPTER]
TIMEBASE=1/1000
START=0
END=10000
title=Scene 1
[CHAPTER]
TIMEBASE=1/1000
START=10000
END=40000
title=Intro
[CHAPTER]
TIMEBASE=1/1000
START=40000
END=70000
title=Scene 3
[CHAPTER]
TIMEBASE=1/1000
START=70000
END=90000
title=Credits
`), 0o644); err != nil {
		t.Fatal(err)
	}

	clip := filepath.Join(dir, "clip.mkv")
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=6:duration=90",
		"-i", meta, "-map_metadata", "1",
		"-c:v", "libx264", "-preset", "ultrafast", clip).CombinedOutput()
	if err != nil {
		t.Fatalf("build clip: %v: %s", err, out)
	}

	info, err := NewProber(ffprobe).Probe(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Chapters) != 4 {
		t.Fatalf("read %d chapters, want 4: %+v", len(info.Chapters), info.Chapters)
	}

	var op, ed *Chapter
	for i, c := range info.Chapters {
		switch c.Kind {
		case "op":
			op = &info.Chapters[i]
		case "ed":
			ed = &info.Chapters[i]
		}
	}
	if op == nil || ed == nil {
		t.Fatalf("opening or ending not classified: %+v", info.Chapters)
	}
	if op.Start < 9.9 || op.Start > 10.1 || op.End < 39.9 || op.End > 40.1 {
		t.Errorf("opening = %.2f-%.2f, want 10-40", op.Start, op.End)
	}
	if ed.Start < 69.9 || ed.Start > 70.1 {
		t.Errorf("ending starts at %.2f, want 70", ed.Start)
	}
	// A body chapter is not something to offer skipping.
	for _, c := range info.Chapters {
		if c.Title == "Scene 1" && c.Kind != "" {
			t.Errorf("body chapter classified as %q", c.Kind)
		}
	}
}

// A theme runs a minute or two; a quarter-hour "Intro" is a mislabel, and
// skipping it would take the episode with it.
func TestImplausibleThemeSpanIsNotSkippable(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	dir := t.TempDir()

	// Too long to be a theme, and too short to be one.
	meta := filepath.Join(dir, "chapters.ini")
	if err := os.WriteFile(meta, []byte(`;FFMETADATA1
[CHAPTER]
TIMEBASE=1/1000
START=0
END=250000
title=Intro
[CHAPTER]
TIMEBASE=1/1000
START=250000
END=258000
title=Credits
`), 0o644); err != nil {
		t.Fatal(err)
	}

	clip := filepath.Join(dir, "long.mkv")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=6:duration=260",
		"-i", meta, "-map_metadata", "1",
		"-c:v", "libx264", "-preset", "ultrafast", clip).CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v: %s", err, out)
	}

	info, err := NewProber(ffprobe).Probe(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range info.Chapters {
		if c.Kind != "" {
			t.Errorf("%q spanning %.0fs was offered as %q", c.Title, c.End-c.Start, c.Kind)
		}
	}
}

// Against a real release: set REALFILE to an episode on disk.
func TestRealReleaseChapters(t *testing.T) {
	path := os.Getenv("REALFILE")
	if path == "" {
		t.Skip("set REALFILE")
	}
	_, ffprobe := binaries(t)

	info, err := NewProber(ffprobe).Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration <= 0 {
		t.Fatalf("no duration read from %s", path)
	}
	for _, c := range info.Chapters {
		t.Logf("%-12s %8.2f - %8.2f  kind=%q", c.Title, c.Start, c.End, c.Kind)
		if c.Kind == "" {
			continue
		}
		if span := c.End - c.Start; span < 15 || span > 240 {
			t.Errorf("%q offered as %q but spans %.0fs", c.Title, c.Kind, span)
		}
		if c.End > info.Duration+1 {
			t.Errorf("%q ends at %.2f, past the episode's %.2f", c.Title, c.End, info.Duration)
		}
	}
}

// A file with no chapters is the normal case, not a failure.
func TestProbeWithoutChapters(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	info, err := NewProber(ffprobe).Probe(context.Background(), testMKV(t, ffmpeg, 6))
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Chapters) != 0 {
		t.Errorf("got %d chapters", len(info.Chapters))
	}
}
