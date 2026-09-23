package transcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The renderer is libass. A .srt handed to it loads without error and draws
// nothing, which looks exactly like having no subtitles.
func TestEveryTextFormatIsWrittenAsASS(t *testing.T) {
	for _, codec := range []string{
		"ass", "ssa", "subrip", "srt", "webvtt", "mov_text",
		"text", "subviewer", "microdvd", "jacosub", "sami", "stl",
	} {
		if !Renderable(codec) {
			t.Errorf("%s is a text format and should be renderable", codec)
		}
		if got := subtitleExt(codec); got != "ass" {
			t.Errorf("%s extracts to .%s, want .ass", codec, got)
		}
	}
}

// Only ASS keeps its own styling; everything else is converted, so copying it
// verbatim would write a file the renderer cannot read.
func TestOnlyASSIsCopiedVerbatim(t *testing.T) {
	for _, codec := range []string{"ass", "ssa"} {
		if !isASS(codec) {
			t.Errorf("%s should be copied verbatim", codec)
		}
	}
	for _, codec := range []string{"subrip", "srt", "webvtt", "mov_text"} {
		if isASS(codec) {
			t.Errorf("%s cannot be copied verbatim; it has to be converted", codec)
		}
	}
}

// A picture of text cannot become text. Offering it as a track that renders
// nothing is worse than saying it is not available.
func TestBitmapFormatsAreNotRenderable(t *testing.T) {
	for _, codec := range []string{
		"hdmv_pgs_subtitle", "pgssub", "dvd_subtitle", "dvdsub", "dvb_subtitle", "xsub",
	} {
		if Renderable(codec) {
			t.Errorf("%s is a bitmap format and cannot be rendered as text", codec)
		}
	}
}

// A closed session's entries go, and only that session's.
func TestForgetDropsOnlyThatSession(t *testing.T) {
	s := NewSubtitles("ffmpeg")
	root := t.TempDir()
	closed, open := filepath.Join(root, "1-3"), filepath.Join(root, "1-30")
	for _, dir := range []string{closed, open} {
		p := filepath.Join(dir, "sub-2.ass")
		s.done[p] = struct{}{}
		s.lockFor(p)
	}

	s.Forget(closed)
	if len(s.done) != 1 || len(s.busy) != 1 {
		t.Fatalf("done %d, busy %d; want only the open session's left", len(s.done), len(s.busy))
	}
	if _, ok := s.done[filepath.Join(open, "sub-2.ass")]; !ok {
		t.Fatal("a sibling whose name starts the same was forgotten")
	}
}

// Closing a session removes its directory and the id is the episode, so the
// next play looks up the same path. A remembered extraction must not hand back
// a path whose file was deleted with the directory.
func TestExtractRedoesWorkWhenTheFileIsGone(t *testing.T) {
	ffmpeg, _ := binaries(t)
	clip := testMKV(t, ffmpeg, 6)

	dir := t.TempDir()
	s := NewSubtitles(ffmpeg)

	first, err := s.Extract(context.Background(), clip, dir, 2, "ass", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("nothing was written: %v", err)
	}

	// What closing the session does.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	again, err := s.Extract(context.Background(), clip, dir, 2, "ass", true)
	if err != nil {
		t.Fatalf("second extraction failed: %v", err)
	}
	if _, err := os.Stat(again); err != nil {
		t.Fatalf("handed back a path with no file behind it: %v", err)
	}
}
