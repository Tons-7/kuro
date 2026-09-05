package player

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Anime4K ships one shader per network size. Mode picks which passes run; size
// picks how large their CNNs are, each step up roughly doubling GPU cost. The
// second upscale is always M — it runs after the downscale passes, where a
// larger network buys nothing.
const (
	shaderClamp      = "Anime4K_Clamp_Highlights.glsl"
	shaderDownscale2 = "Anime4K_AutoDownscalePre_x2.glsl"
	shaderDownscale4 = "Anime4K_AutoDownscalePre_x4.glsl"
	shaderUpscaleM   = "Anime4K_Upscale_CNN_x2_M.glsl"
	shaderRestoreM   = "Anime4K_Restore_CNN_M.glsl"
	shaderSoftM      = "Anime4K_Restore_CNN_Soft_M.glsl"
)

var shaderSizes = map[string]bool{"S": true, "M": true, "L": true, "VL": true, "UL": true}

// Anime4KChain returns the shader file names for a mode and size, in order.
// An unknown mode or size falls back to the upstream defaults.
func Anime4KChain(mode, size string) []string {
	size = strings.ToUpper(strings.TrimSpace(size))
	if !shaderSizes[size] {
		size = "M"
	}

	restore := fmt.Sprintf("Anime4K_Restore_CNN_%s.glsl", size)
	soft := fmt.Sprintf("Anime4K_Restore_CNN_Soft_%s.glsl", size)
	upscale := fmt.Sprintf("Anime4K_Upscale_CNN_x2_%s.glsl", size)
	denoise := fmt.Sprintf("Anime4K_Upscale_Denoise_CNN_x2_%s.glsl", size)

	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "B":
		return []string{shaderClamp, soft, upscale, shaderDownscale2, shaderDownscale4, shaderUpscaleM}
	case "C":
		return []string{shaderClamp, denoise, shaderDownscale2, shaderDownscale4, shaderUpscaleM}
	case "A+A":
		return []string{shaderClamp, restore, upscale, shaderRestoreM, shaderDownscale2, shaderDownscale4, shaderUpscaleM}
	case "B+B":
		return []string{shaderClamp, soft, upscale, shaderDownscale2, shaderDownscale4, shaderSoftM, shaderUpscaleM}
	case "C+A":
		return []string{shaderClamp, denoise, shaderDownscale2, shaderDownscale4, shaderRestoreM, shaderUpscaleM}
	default:
		return []string{shaderClamp, restore, upscale, shaderDownscale2, shaderDownscale4, shaderUpscaleM}
	}
}

// anime4kArgs resolves the chain against the shader directory, returning nothing
// if a shader is missing: upscaling is a nicety, not worth refusing to play over.
func (m *MPV) anime4kArgs(mode, size string) []string {
	if m.shaders == "" {
		return nil
	}

	chain := Anime4KChain(mode, size)
	args := make([]string, 0, len(chain))
	for _, name := range chain {
		path := filepath.Join(m.shaders, name)
		if _, err := os.Stat(path); err != nil {
			m.log.Warn("anime4k shaders unavailable, playing without them",
				"missing", name, "dir", m.shaders)
			return nil
		}
		// One append per shader avoids the list separator, which differs
		// between platforms and has no escape for a path containing it.
		args = append(args, "--glsl-shaders-append="+path)
	}
	return args
}
