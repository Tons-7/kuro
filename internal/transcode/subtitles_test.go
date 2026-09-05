package transcode

import (
	"context"
	"os"
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
