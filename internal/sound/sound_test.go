package sound

import (
	"strings"
	"testing"
)

func TestNothingAskedForIsNoChain(t *testing.T) {
	if Chain(Options{}, 2) != "" || Chain(Options{Voice: "off"}, 6) != "" || MPVArgs(Options{}) != nil || VLCArgs(Options{Voice: "off"}) != nil {
		t.Fatal("the zero value changed something")
	}
	if (Options{Voice: "off"}).Any() || !(Options{Mono: true}).Any() || (Options{Voice: "medium"}).Level() != "medium" || (Options{}).Level() != "off" {
		t.Fatal("Any or Level is wrong")
	}
	if !ValidVoice("off") || !ValidVoice("") || !ValidVoice("high") || ValidVoice("loud") {
		t.Fatal("ValidVoice is wrong")
	}
}

func TestChainFollowsTheLayout(t *testing.T) {
	stereo := Chain(Options{Voice: "medium"}, 2)
	if !strings.HasPrefix(stereo, "dialoguenhance=original=0:enhance=2:voice=2,") || !strings.Contains(stereo, "pan=stereo|FL=FL+0.498*FC") || !strings.HasSuffix(stereo, limiter) {
		t.Errorf("stereo medium = %s", stereo)
	}
	if high := Chain(Options{Voice: "high"}, 2); !strings.Contains(high, "enhance=3") || !strings.Contains(high, "FL=FL+0.994*FC") {
		t.Errorf("stereo high = %s", high)
	}
	surround := Chain(Options{Voice: "high"}, 6)
	if !strings.HasPrefix(surround, "aformat=channel_layouts=5.1,") || !strings.Contains(surround, "FL=0.414*FL+1.166*FC+0.293*BL") || strings.Contains(surround, "dialoguenhance") {
		t.Errorf("surround high = %s", surround)
	}
	if eight := Chain(Options{Voice: "medium"}, 8); !strings.Contains(eight, "0.585*FC") {
		t.Errorf("7.1 is folded to 5.1 first and lifted the same: %s", eight)
	}
	if m := Chain(Options{Voice: "medium"}, 1); strings.Contains(m, "pan=") || strings.Contains(m, "dialoguenhance") || !strings.Contains(m, "equalizer") {
		t.Errorf("mono has nothing to lift the voice over: %s", m)
	}
}

func TestTheOtherEffectsStackInOrder(t *testing.T) {
	all := Chain(Options{Voice: "medium", Night: true, Headphones: true, Mono: true}, 2)
	for _, want := range []string{"dialoguenhance", "acompressor", "0.5*FL+0.5*FR", limiter} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in %s", want, all)
		}
	}
	if strings.Contains(all, "crossfeed") {
		t.Error("crossfeed is pointless on a mono fold")
	}
	if i, j := strings.Index(all, "acompressor"), strings.Index(all, "0.5*FL+0.5*FR"); i > j {
		t.Error("the fold to mono comes after the compressor")
	}
	// Without the voice, a surround track is still folded down before the rest.
	if c := Chain(Options{Night: true}, 6); !strings.HasPrefix(c, "aformat=channel_layouts=stereo,acompressor") {
		t.Errorf("surround night = %s", c)
	}
	if c := Chain(Options{Headphones: true}, 2); c != headphones+","+limiter {
		t.Errorf("headphones alone = %s", c)
	}
}

func TestMPVAndVLCRenderings(t *testing.T) {
	mpv := MPVArgs(Options{Voice: "medium", Night: true})
	if len(mpv) != 2 || mpv[0] != "--audio-channels=stereo" || !strings.HasPrefix(mpv[1], "--af=lavfi=[dialoguenhance") || !strings.HasSuffix(mpv[1], "]") {
		t.Errorf("mpv = %v", mpv)
	}

	vlc := strings.Join(VLCArgs(Options{Voice: "high", Night: true, Headphones: true}), " ")
	for _, want := range []string{"--audio-filter=equalizer:compressor", "--equalizer-preamp=0", "--equalizer-bands=0 0 0 0 5 7 3 0 0 0", "--compressor-ratio=3", "--stereo-mode=6"} {
		if !strings.Contains(vlc, want) {
			t.Errorf("vlc missing %s in %s", want, vlc)
		}
	}
	if v := strings.Join(VLCArgs(Options{Mono: true, Headphones: true}), " "); v != "--stereo-mode=7" {
		t.Errorf("mono wins over headphones in VLC: %s", v)
	}
}

// The volume boost is its own switch, last in the chain and limited.
func TestVolumeBoostIsSeparateFromTheVoice(t *testing.T) {
	c := Chain(Options{Boost: "medium"}, 2)
	if c != "volume=6dB,"+limiter || Chain(Options{Boost: "high"}, 2) != "volume=12dB,"+limiter {
		t.Errorf("boost alone = %s", c)
	}
	both := Chain(Options{Voice: "medium", Boost: "high"}, 2)
	if i, j := strings.Index(both, "dialoguenhance"), strings.Index(both, "volume=12dB"); i < 0 || j < i {
		t.Errorf("the boost comes after the voice: %s", both)
	}
	if !ValidBoost("off") || ValidBoost("max") || (Options{Boost: "off"}).Any() {
		t.Error("ValidBoost or Any is wrong for the boost")
	}
	if v := strings.Join(VLCArgs(Options{Boost: "medium"}), " "); v != "--gain=1.99526" && !strings.HasPrefix(v, "--gain=1.995") {
		t.Errorf("vlc boost = %s", v)
	}
	if m := MPVArgs(Options{Boost: "high"}); len(m) != 2 || !strings.Contains(m[1], "volume=12dB") {
		t.Errorf("mpv boost = %v", m)
	}
}

func TestUnknownChannelCountIsTreatedAsStereo(t *testing.T) {
	if Chain(Options{Mono: true}, 0) != Chain(Options{Mono: true}, 2) {
		t.Error("an unprobed track is not handled as stereo")
	}
}
