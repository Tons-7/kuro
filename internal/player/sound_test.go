package player

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"

	"kuro/internal/sound"
)

func TestMPVAndVLCCarryTheSoundOptions(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := Options{URL: "http://127.0.0.1:1/s", Sound: sound.Options{Voice: "medium", Mono: true}}

	vlc := strings.Join(NewVLC("", log).args(opts, control{port: 1, password: "pw"}), " ")
	if !strings.Contains(vlc, "--audio-filter=equalizer") || !strings.Contains(vlc, "--stereo-mode=7") {
		t.Errorf("vlc args = %s", vlc)
	}
	if plain := strings.Join(NewVLC("", log).args(Options{URL: "x"}, control{}), " "); strings.Contains(plain, "--audio-filter") || strings.Contains(plain, "stereo-mode") {
		t.Errorf("nothing asked for, yet %s", plain)
	}
}

// mpv runs ffmpeg's filters itself: the graph kuro hands it must be one its build accepts.
func TestMPVAcceptsTheSoundGraph(t *testing.T) {
	mpv, ffmpeg := binaries(t)
	clip := testClip(t, ffmpeg, 3)
	args := append([]string{"--no-config", "--ao=null", "--vo=null", "--length=1", "--really-quiet"},
		sound.MPVArgs(sound.Options{Voice: "high", Night: true, Headphones: true})...)
	args = append(args, clip)
	out, err := exec.Command(mpv, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("mpv refused the graph: %v\n%s", err, out)
	}
	if _, err := os.Stat(clip); err != nil {
		t.Fatal(err)
	}
}
