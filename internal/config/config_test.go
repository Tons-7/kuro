package config

import "testing"

func TestRedirectURI(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"127.0.0.1:4321", "http://localhost:4321/callback"},
		{"0.0.0.0:8080", "http://localhost:8080/callback"},
		{":4321", "http://localhost:4321/callback"},
		{"garbage", "http://localhost:4321/callback"},
	}

	// Must match the URL registered on AniList exactly, including the port.
	for _, tt := range tests {
		if got := (Config{Addr: tt.addr}).RedirectURI(); got != tt.want {
			t.Errorf("addr %q gave %q, want %q", tt.addr, got, tt.want)
		}
	}
}
