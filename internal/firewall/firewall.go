// Package firewall lets other devices on the home network reach kuro. Windows
// asks about a program only the first time it listens beyond loopback, and an
// answer given once (or a network marked Public) silently blocks every phone
// afterwards, so kuro checks and, when asked, adds a rule of its own.
package firewall

import (
	"fmt"
	"strings"
)

// Network is one connection as Windows classifies it. Public is what Windows
// picks for a network it has not been told to trust.
type Network struct {
	Name      string `json:"name"`
	Interface string `json:"interface"`
	Category  string `json:"category"`
}

// Rule is an inbound firewall rule touching kuro: its own port rule, or one
// Windows made for kuro.exe when its prompt was answered.
type Rule struct {
	Name     string `json:"name"`
	Action   string `json:"action"`
	Profiles string `json:"profiles"`
	Enabled  bool   `json:"enabled"`
}

type Status struct {
	Supported bool      `json:"supported"`
	Networks  []Network `json:"networks"`
	// PortRule is kuro's own rule for its port, if added.
	PortRule *Rule `json:"portRule,omitempty"`
	// Program lists the rules for kuro.exe; a Block among them beats any allow.
	Program []Rule `json:"program"`

	// Off Windows kuro only reports: which firewall is running, and what to do
	// about it. Changing it there needs root and differs per distribution.
	Firewall string `json:"firewall,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Command  string `json:"command,omitempty"`
}

// Blocked reports a block rule for kuro.exe that is on, which wins over every
// allow rule whatever the profile.
func (s Status) Blocked() bool {
	for _, r := range s.Program {
		if r.Enabled && strings.EqualFold(r.Action, "Block") {
			return true
		}
	}
	return false
}

// OnPublic reports a connected network Windows treats as Public.
func (s Status) OnPublic() bool {
	for _, n := range s.Networks {
		if strings.EqualFold(n.Category, "Public") {
			return true
		}
	}
	return false
}

// Allows reports whether an enabled allow rule covers the profile.
func (s Status) Allows(profile string) bool {
	rules := append([]Rule{}, s.Program...)
	if s.PortRule != nil {
		rules = append(rules, *s.PortRule)
	}
	for _, r := range rules {
		if !r.Enabled || !strings.EqualFold(r.Action, "Allow") {
			continue
		}
		if strings.EqualFold(r.Profiles, "Any") || strings.Contains(strings.ToLower(r.Profiles), strings.ToLower(profile)) {
			return true
		}
	}
	return false
}

// Reachable reports whether a phone on each connected network would get in.
func (s Status) Reachable() bool {
	if !s.Supported {
		return true
	}
	if s.Blocked() || len(s.Networks) == 0 {
		return false
	}
	for _, n := range s.Networks {
		profile := n.Category
		if strings.EqualFold(profile, "DomainAuthenticated") {
			profile = "Domain"
		}
		if !s.Allows(profile) {
			return false
		}
	}
	return true
}

// RuleName carries the port, so a changed port gets a rule of its own.
func RuleName(port int) string { return fmt.Sprintf("kuro (LAN, TCP %d)", port) }

// ErrCancelled: the administrator prompt was declined.
var ErrCancelled = fmt.Errorf("the administrator prompt was declined")
