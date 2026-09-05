package player

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestChainMatchesUpstreamModes(t *testing.T) {
	cases := map[string][]string{
		"A": {
			"Anime4K_Clamp_Highlights.glsl",
			"Anime4K_Restore_CNN_VL.glsl",
			"Anime4K_Upscale_CNN_x2_VL.glsl",
			"Anime4K_AutoDownscalePre_x2.glsl",
			"Anime4K_AutoDownscalePre_x4.glsl",
			"Anime4K_Upscale_CNN_x2_M.glsl",
		},
		"B": {
			"Anime4K_Clamp_Highlights.glsl",
			"Anime4K_Restore_CNN_Soft_VL.glsl",
			"Anime4K_Upscale_CNN_x2_VL.glsl",
			"Anime4K_AutoDownscalePre_x2.glsl",
			"Anime4K_AutoDownscalePre_x4.glsl",
			"Anime4K_Upscale_CNN_x2_M.glsl",
		},
		"C": {
			"Anime4K_Clamp_Highlights.glsl",
			"Anime4K_Upscale_Denoise_CNN_x2_VL.glsl",
			"Anime4K_AutoDownscalePre_x2.glsl",
			"Anime4K_AutoDownscalePre_x4.glsl",
			"Anime4K_Upscale_CNN_x2_M.glsl",
		},
		"A+A": {
			"Anime4K_Clamp_Highlights.glsl",
			"Anime4K_Restore_CNN_VL.glsl",
			"Anime4K_Upscale_CNN_x2_VL.glsl",
			"Anime4K_Restore_CNN_M.glsl",
			"Anime4K_AutoDownscalePre_x2.glsl",
			"Anime4K_AutoDownscalePre_x4.glsl",
			"Anime4K_Upscale_CNN_x2_M.glsl",
		},
		"B+B": {
			"Anime4K_Clamp_Highlights.glsl",
			"Anime4K_Restore_CNN_Soft_VL.glsl",
			"Anime4K_Upscale_CNN_x2_VL.glsl",
			"Anime4K_AutoDownscalePre_x2.glsl",
			"Anime4K_AutoDownscalePre_x4.glsl",
			"Anime4K_Restore_CNN_Soft_M.glsl",
			"Anime4K_Upscale_CNN_x2_M.glsl",
		},
		"C+A": {
			"Anime4K_Clamp_Highlights.glsl",
			"Anime4K_Upscale_Denoise_CNN_x2_VL.glsl",
			"Anime4K_AutoDownscalePre_x2.glsl",
			"Anime4K_AutoDownscalePre_x4.glsl",
			"Anime4K_Restore_CNN_M.glsl",
			"Anime4K_Upscale_CNN_x2_M.glsl",
		},
	}

	for mode, want := range cases {
		got := Anime4KChain(mode, "VL")
		if !slices.Equal(got, want) {
			t.Errorf("mode %s:\n got  %v\n want %v", mode, got, want)
		}
	}
}

// The size applies to the primary passes only; the trailing upscale stays M
// because it runs after the auto-downscale, where a bigger network is wasted.
func TestSizeAppliesToPrimaryPassesOnly(t *testing.T) {
	for _, size := range []string{"S", "M", "L", "VL", "UL"} {
		chain := Anime4KChain("A", size)
		if got := chain[1]; got != "Anime4K_Restore_CNN_"+size+".glsl" {
			t.Errorf("size %s: restore pass = %s", size, got)
		}
		if got := chain[len(chain)-1]; got != shaderUpscaleM {
			t.Errorf("size %s: final pass = %s, want the M network", size, got)
		}
	}
}

func TestUnknownModeAndSizeFallBack(t *testing.T) {
	if got, want := Anime4KChain("", ""), Anime4KChain("A", "M"); !slices.Equal(got, want) {
		t.Errorf("empty settings = %v, want mode A at M", got)
	}
	if got, want := Anime4KChain("Z", "XXL"), Anime4KChain("A", "M"); !slices.Equal(got, want) {
		t.Errorf("nonsense settings = %v, want mode A at M", got)
	}
	// Casing comes from a settings field, not a fixed list.
	if got, want := Anime4KChain("c+a", "vl"), Anime4KChain("C+A", "VL"); !slices.Equal(got, want) {
		t.Errorf("lowercase mode produced %v", got)
	}
}

// Every name the chain can produce must exist in the upstream release, or the
// shader argument silently disables upscaling at playback.
func TestEveryChainNameIsAKnownShader(t *testing.T) {
	// The full file list from Anime4K v4.0.1.
	known := map[string]bool{}
	for _, base := range []string{
		"Anime4K_Clamp_Highlights", "Anime4K_AutoDownscalePre_x2", "Anime4K_AutoDownscalePre_x4",
	} {
		known[base+".glsl"] = true
	}
	for _, base := range []string{
		"Anime4K_Restore_CNN", "Anime4K_Restore_CNN_Soft",
		"Anime4K_Upscale_CNN_x2", "Anime4K_Upscale_Denoise_CNN_x2",
	} {
		for _, size := range []string{"S", "M", "L", "VL", "UL"} {
			known[base+"_"+size+".glsl"] = true
		}
	}

	for _, mode := range []string{"A", "B", "C", "A+A", "B+B", "C+A"} {
		for _, size := range []string{"S", "M", "L", "VL", "UL"} {
			for _, name := range Anime4KChain(mode, size) {
				if !known[name] {
					t.Errorf("mode %s size %s references unknown shader %s", mode, size, name)
				}
			}
		}
	}
}

func newMPV(t *testing.T, shaders string) *MPV {
	t.Helper()
	m := New(filepath.Join(t.TempDir(), "mpv.exe"), `\\.\pipe\kuro-test`,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.shaders = shaders
	return m
}

func TestArgsAreOneAppendPerShader(t *testing.T) {
	dir := t.TempDir()
	for _, name := range Anime4KChain("A", "VL") {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("// shader"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	args := newMPV(t, dir).anime4kArgs("A", "VL")
	if len(args) != 6 {
		t.Fatalf("got %d args, want one per shader", len(args))
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "--glsl-shaders-append=") {
			t.Errorf("arg %q does not append", a)
		}
		// A list separator would be ambiguous with a Windows drive letter.
		if strings.Count(a, "--glsl-shaders-append=") != 1 {
			t.Errorf("arg %q packs several shaders", a)
		}
	}
}

// A missing shader must not stop the episode playing.
func TestMissingShadersDisableUpscalingRatherThanFailing(t *testing.T) {
	dir := t.TempDir()
	// Everything but the last pass.
	chain := Anime4KChain("A", "VL")
	for _, name := range chain[:len(chain)-1] {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("// shader"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if args := newMPV(t, dir).anime4kArgs("A", "VL"); args != nil {
		t.Fatalf("got %v, want no shader args when the chain is incomplete", args)
	}
	if args := newMPV(t, "").anime4kArgs("A", "VL"); args != nil {
		t.Fatalf("got %v with no shader directory", args)
	}
}

func TestShadersDirectoryDefaultsBesideTheBinary(t *testing.T) {
	m := New(filepath.Join("C:", "kuro", "bin", "mpv.exe"), "",
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	want := filepath.Join("C:", "kuro", "bin", "shaders")
	if m.shaders != want {
		t.Errorf("shaders dir = %q, want %q", m.shaders, want)
	}
}
