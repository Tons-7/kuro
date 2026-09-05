package server

import (
	"context"
	"testing"

	"kuro/internal/config"
	"kuro/internal/store"
)

func TestPreferredGroupsSettingReachesPlaybackPreferences(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := context.Background()

	if p := h.server.preferences(ctx); len(p.PreferGroups) != 0 {
		t.Fatalf("default groups = %v", p.PreferGroups)
	}
	res, body := h.postJSON(t, "/api/prefs", map[string]any{"key": "release.prefer_groups", "value": `["SubsPlease","Erai-raws"]`})
	if res.StatusCode != 200 {
		t.Fatalf("HTTP %d %v", res.StatusCode, body)
	}
	p := h.server.preferences(ctx)
	if len(p.PreferGroups) != 2 || p.PreferGroups[0] != "SubsPlease" {
		t.Fatalf("groups = %v", p.PreferGroups)
	}
	_, prefs := h.do(t, "GET", "/api/prefs")
	if prefs["effective"].(map[string]any)["release.prefer_groups"] != `["SubsPlease","Erai-raws"]` {
		t.Fatalf("effective = %v", prefs["effective"])
	}
}

func TestAssignAndForgetLocalFiles(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := context.Background()
	seedShow(t, h, 1, 12)

	stamp, _ := h.store.NextScanStamp(ctx)
	if _, err := h.store.SaveLocalFiles(ctx, stamp, []store.LocalFile{
		{Path: `D:\lib\mystery.mkv`, Size: 1, Modified: 1},
		{Path: `D:\lib\gone.mkv`, Size: 1, Modified: 1},
	}); err != nil {
		t.Fatal(err)
	}
	files, _ := h.store.LocalFiles(ctx, 0, true, store.Paging{Page: 1, PerPage: 10})
	if len(files.Items) != 2 {
		t.Fatalf("unmatched = %d", len(files.Items))
	}
	var mystery, gone int
	for _, f := range files.Items {
		if f.Path == `D:\lib\mystery.mkv` {
			mystery = f.ID
		} else {
			gone = f.ID
		}
	}

	res, body := h.postJSON(t, "/api/local/assign", map[string]any{"id": mystery, "animeId": 1, "episode": 7})
	if res.StatusCode != 200 {
		t.Fatalf("HTTP %d %v", res.StatusCode, body)
	}
	if f, _ := h.store.LocalEpisode(ctx, 1, 7); f.ID != mystery || f.Confidence != 1 {
		t.Fatalf("assigned file = %+v", f)
	}
	if res, _ := h.postJSON(t, "/api/local/assign", map[string]any{"animeId": 1, "episode": 7}); res.StatusCode != 400 {
		t.Fatalf("missing id: HTTP %d", res.StatusCode)
	}

	// A later scan without gone.mkv flags it; forgetting drops only that row.
	next, _ := h.store.NextScanStamp(ctx)
	h.store.SaveLocalFiles(ctx, next, []store.LocalFile{{Path: `D:\lib\mystery.mkv`, Size: 1, Modified: 1, AnimeID: 1, Episode: 7}})
	h.store.SweepLocal(ctx, next, []string{`D:\lib`})
	_, stats := h.do(t, "GET", "/api/local")
	if stats["stats"].(map[string]any)["missing"] != float64(1) {
		t.Fatalf("stats = %v", stats)
	}
	res, body = h.do(t, "POST", "/api/local/forget")
	if res.StatusCode != 200 || body["forgotten"] != float64(1) {
		t.Fatalf("HTTP %d %v", res.StatusCode, body)
	}
	if f, _ := h.store.LocalFile(ctx, gone); f.ID != 0 {
		t.Fatal("missing file still recorded")
	}
	if f, _ := h.store.LocalFile(ctx, mystery); f.ID == 0 {
		t.Fatal("present file was forgotten")
	}
}
