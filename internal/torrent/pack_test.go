package torrent

import "testing"

func mb(n int64) int64 { return n << 20 }

// Chinese groups number in a bracket of their own; the marked form drops the season.
func TestEpisodeInNameReadsBracketsAndMarkedForms(t *testing.T) {
	for name, want := range map[string]int{
		"[VCB-Studio] Shingeki no Kyojin [05][Ma10p_1080p][x265_flac]": 5,
		"[Nekomoe kissaten][Show][05][1080p][JPSC]":                    5,
		"[Sakurato] Show [05v2][AVC-8bit 1080p AAC][CHS]":              5,
		"[Group] Show EP05 [1080p]":                                    5,
		"[Group] Show E05 [1080p]":                                     5,
		"Show.S02.E05.1080p":                                           5,
		"Show.EP05.2160p.WEB-DL":                                       5,
		"[Group] Show (2024) [1080p]":                                  0,
		"Show - 12.5":                                                  0,
		"Show - 12":                                                    12,
	} {
		if got := episodeInName(name); got != want {
			t.Errorf("%q = %d, want %d", name, got, want)
		}
	}
}

// A VCB pack is playable, and a lone bracket-numbered file for another episode does not play as episode 1.
func TestBracketNumberedPackAndLoneFile(t *testing.T) {
	var pack []File
	for n := 1; n <= 12; n++ {
		pack = append(pack, File{Name: "[VCB-Studio] Show [" + twoDigits(n) + "][Ma10p_1080p][x265_flac].mkv", Length: mb(900)})
	}
	got, _, ok := PickEpisode(pack, 5)
	if !ok || got.Name != pack[4].Name {
		t.Fatalf("picked %q, %v", got.Name, ok)
	}
	lone := []File{{Name: "[Nekomoe kissaten] Show [05][1080p].mkv", Length: mb(900)}}
	if _, _, ok := PickEpisode(lone, 1); ok {
		t.Error("episode 5's file was taken as episode 1")
	}
	if _, _, ok := PickEpisode(lone, 5); !ok {
		t.Error("episode 5's own file was refused")
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// Extras under their own folder, or named as openings, previews and menus, never play as an episode;
// among files numbered alike the largest is the episode.
func TestExtrasAreNeverTheEpisode(t *testing.T) {
	files := []File{
		{Name: "Show/Extras/[G] Show - NCOP 01 [1080p].mkv", Length: mb(90)},
		{Name: "Show/Extras/[G] Show - NCED 01 [1080p].mkv", Length: mb(90)},
		{Name: "Show/Extras/[G] Show - PV 01 [1080p].mkv", Length: mb(40)},
		{Name: "Show/Extras/[G] Show - PV 02 [1080p].mkv", Length: mb(40)},
		{Name: "Show/Extras/[G] Show - CM 01 [1080p].mkv", Length: mb(20)},
		{Name: "Show/Extras/[G] Show - Menu 01 [1080p].mkv", Length: mb(30)},
		{Name: "Show/[G] Show - 01 [1080p].mkv", Length: mb(1400)},
		{Name: "Show/[G] Show - 01 [1080p] (preview).mkv", Length: mb(300)},
		{Name: "Show/[G] Show - 02 [1080p].mkv", Length: mb(1400)},
		{Name: "Show/SPs/[G] Show - SP01 [1080p].mkv", Length: mb(600)},
	}
	got, _, ok := PickEpisode(files, 1)
	if !ok || got.Name != "Show/[G] Show - 01 [1080p].mkv" {
		t.Fatalf("picked %q, %v", got.Name, ok)
	}
	// A folder of nothing but extras names no episode.
	if _, _, ok := PickEpisode(files[:6], 1); ok {
		t.Error("an opening was taken as episode 1")
	}
}

// A recap "12.5" is not episode 12, whichever sorts first.
func TestRecapIsNotTheEpisode(t *testing.T) {
	files := []File{
		{Name: "Show - 11.mkv", Length: mb(700)},
		{Name: "Show - 12.5.mkv", Length: mb(700)},
		{Name: "Show - 12.mkv", Length: mb(700)},
	}
	got, _, ok := PickEpisode(files, 12)
	if !ok || got.Name != "Show - 12.mkv" {
		t.Fatalf("picked %q, %v", got.Name, ok)
	}
}

// A pack of a later cour may count from one or carry on the season's count: its own files say which.
func TestPackNumberingIsReadOffItsFiles(t *testing.T) {
	numbered := func(from, to int) []File {
		var out []File
		for n := from; n <= to; n++ {
			out = append(out, File{Name: "[G] Mushoku Tensei - " + twoDigits(n) + " [1080p].mkv", Length: mb(1000)})
		}
		return out
	}
	// Part 2's episode 12 is "23" counted through the season (part 1 had 11).
	if got, _, ok := PickNumbered(numbered(12, 23), 12, []int{12, 23}); !ok || got.Name != "[G] Mushoku Tensei - 23 [1080p].mkv" {
		t.Errorf("season count: picked %q, %v", got.Name, ok)
	}
	if got, _, ok := PickNumbered(numbered(1, 12), 12, []int{12, 23}); !ok || got.Name != "[G] Mushoku Tensei - 12 [1080p].mkv" {
		t.Errorf("own count: picked %q, %v", got.Name, ok)
	}
	// Half a pack still tells: files 7-12 hold episode 8 under its own count, not 19.
	if got, _, ok := PickNumbered(numbered(7, 12), 8, []int{8, 19}); !ok || got.Name != "[G] Mushoku Tensei - 08 [1080p].mkv" {
		t.Errorf("partial pack: picked %q, %v", got.Name, ok)
	}
	// A bare pack of a later cour: only the carried-over number is offered, and the pack counts that way.
	if got, _, ok := PickNumbered(numbered(12, 23), 12, []int{23}); !ok || got.Name != "[G] Mushoku Tensei - 23 [1080p].mkv" {
		t.Errorf("bare later cour: picked %q, %v", got.Name, ok)
	}
}

// Older fansubs shipped as .ogm and iTunes rips as .m4v; they play like any other container.
func TestOtherVideoContainersCount(t *testing.T) {
	files := []File{{Name: "Show - 01.ogm", Length: mb(300)}, {Name: "Show - 02.m4v", Length: mb(300)}}
	if got, _, ok := PickEpisode(files, 2); !ok || got.Name != "Show - 02.m4v" {
		t.Errorf("picked %q, %v", got.Name, ok)
	}
}
