package firewall

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

// A phone gets in only when every connected network's profile is allowed and
// nothing blocks kuro.exe outright.
func TestReachable(t *testing.T) {
	home := []Network{{Name: "Home", Category: "Private"}}
	cafe := []Network{{Name: "Cafe", Category: "Public"}}
	port := &Rule{Action: "Allow", Profiles: "Private, Domain", Enabled: true}

	cases := []struct {
		name string
		st   Status
		want bool
	}{
		{"private network, port rule", Status{Supported: true, Networks: home, PortRule: port}, true},
		{"public network, private-only rule", Status{Supported: true, Networks: cafe, PortRule: port}, false},
		{"public network, prompt allowed public", Status{Supported: true, Networks: cafe,
			Program: []Rule{{Action: "Allow", Profiles: "Public", Enabled: true}}}, true},
		{"a block rule beats the allow", Status{Supported: true, Networks: home, PortRule: port,
			Program: []Rule{{Action: "Block", Profiles: "Private", Enabled: true}}}, false},
		{"disabled rule counts for nothing", Status{Supported: true, Networks: home,
			PortRule: &Rule{Action: "Allow", Profiles: "Private", Enabled: false}}, false},
		{"no rule at all", Status{Supported: true, Networks: home}, false},
		{"not Windows", Status{}, true},
	}
	for _, c := range cases {
		if got := c.st.Reachable(); got != c.want {
			t.Errorf("%s: Reachable = %v, want %v", c.name, got, c.want)
		}
	}
	if !(Status{Networks: cafe}).OnPublic() {
		t.Error("a Public network was not noticed")
	}
}

// Reads the real firewall; changes nothing.
func TestCheckReadsThisMachine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	// Point it at an installed kuro.exe to see the rules Windows made for it.
	exe := os.Getenv("KURO_FIREWALL_EXE")
	if exe == "" {
		exe, _ = os.Executable()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := Check(ctx, 4321, exe)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Supported {
		t.Error("not marked supported on Windows")
	}
	for _, n := range st.Networks {
		if n.Category == "" {
			t.Errorf("network %+v has no category", n)
		}
	}
	t.Logf("%+v", st)
}
