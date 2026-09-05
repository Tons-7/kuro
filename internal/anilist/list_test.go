package anilist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestFuzzyDateString(t *testing.T) {
	tests := []struct {
		name string
		date FuzzyDate
		want string
	}{
		{"complete", FuzzyDate{ptr(2026), ptr(8), ptr(8)}, "2026-08-08"},
		{"year only", FuzzyDate{Year: ptr(2026)}, "2026-00-00"},
		{"year and month", FuzzyDate{Year: ptr(2026), Month: ptr(3)}, "2026-03-00"},
		{"empty", FuzzyDate{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.date.String(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// An entry appears once per status list and once per custom list it belongs
// to. A real 500-entry account returns roughly 1,320 references.
func TestListDeduplicatesAcrossCustomLists(t *testing.T) {
	const body = `{"data":{"MediaListCollection":{"lists":[
      {"name":"Watching","entries":[
        {"id":1,"mediaId":100,"progress":7,"media":{"id":100}},
        {"id":2,"mediaId":200,"progress":3,"media":{"id":200}}]},
      {"name":"Favourites","isCustomList":true,"entries":[
        {"id":1,"mediaId":100,"progress":7,"media":{"id":100}}]},
      {"name":"Rewatch","isCustomList":true,"entries":[
        {"id":1,"mediaId":100,"progress":7,"media":{"id":100}},
        {"id":2,"mediaId":200,"progress":3,"media":{"id":200}}]}]}}}`

	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})

	entries, err := c.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (5 references collapse to 2)", len(entries))
	}

	seen := map[int]bool{}
	for _, e := range entries {
		if seen[e.ID] {
			t.Fatalf("entry %d returned twice", e.ID)
		}
		seen[e.ID] = true
	}
}

// Omitted variables leave a field untouched; an explicit null clears it. A
// fixed argument list would wipe scores, notes and dates on every write.
func TestSetProgressOmitsUnsetFields(t *testing.T) {
	var vars map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		vars = req.Variables
		io.WriteString(w, `{"data":{"SaveMediaListEntry":{"id":1}}}`)
	})

	if _, err := c.SetProgress(context.Background(), 100, 7, "", nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	// repeat and score included: a zero would wipe what was built up on the site.
	for _, key := range []string{"status", "completedAt", "repeat", "scoreRaw"} {
		if _, present := vars[key]; present {
			t.Fatalf("%q was sent despite being unset", key)
		}
	}
	if vars["progress"] != float64(7) {
		t.Fatalf("progress = %v, want 7", vars["progress"])
	}

	// The rewatch count is always known, so it always travels — including the
	// bump that finishing a rewatch makes.
	if _, err := c.SetProgress(context.Background(), 100, 12, "COMPLETED", &FuzzyDate{ptr(2026), ptr(8), ptr(8)}, 2, 85); err != nil {
		t.Fatal(err)
	}
	if vars["status"] != "COMPLETED" {
		t.Fatalf("status = %v", vars["status"])
	}
	if _, present := vars["completedAt"]; !present {
		t.Fatal("completedAt was not sent when supplied")
	}
	if vars["repeat"] != float64(2) {
		t.Fatalf("repeat = %v, want 2", vars["repeat"])
	}
	// Raw, so the account's display format (5 stars, 10 points…) is irrelevant.
	if vars["scoreRaw"] != float64(85) {
		t.Fatalf("scoreRaw = %v, want 85", vars["scoreRaw"])
	}
	if _, present := vars["score"]; present {
		t.Fatal("a formatted score was sent alongside the raw one")
	}
}

func TestSearchClampsLimit(t *testing.T) {
	var vars map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		vars = req.Variables
		io.WriteString(w, `{"data":{"Page":{"media":[]}}}`)
	})

	// perPage is silently clamped to 50 server-side, so asking for more is a bug.
	for _, in := range []int{0, -5, 500} {
		c.Search(context.Background(), "frieren", in)
		if got := vars["perPage"].(float64); got > 50 || got <= 0 {
			t.Fatalf("limit %d produced perPage %v", in, got)
		}
	}
}

func decode(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
