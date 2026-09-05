package indexer

import "testing"

func TestBuildMakesEachKind(t *testing.T) {
	for _, tt := range []struct {
		kind  string
		adult bool
		name  string
	}{
		{"nyaa", false, "nyaa"},
		{"NYAA", false, "nyaa"},
		{"nyaa", true, "sukebei"},
		{"tokyotosho", false, "tokyotosho"},
	} {
		src, err := Build(tt.kind, "https://example.test/", tt.adult)
		if err != nil {
			t.Fatalf("%s adult=%v: %v", tt.kind, tt.adult, err)
		}
		if src.Name() != tt.name {
			t.Errorf("%s adult=%v: name %q, want %q", tt.kind, tt.adult, src.Name(), tt.name)
		}
	}
}

func TestBuildRejectsWhatItCannotRead(t *testing.T) {
	if _, err := Build("torznab", "https://example.test", false); err == nil {
		t.Error("an unknown type should be refused, not silently searched")
	}
	for _, bad := range []string{"", "example.test", "/feed", "ftp://"} {
		if _, err := Build("nyaa", bad, false); err == nil {
			t.Errorf("url %q should be refused", bad)
		}
	}
}
