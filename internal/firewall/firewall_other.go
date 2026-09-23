//go:build !windows

package firewall

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Check only looks, and never needs root. Most Linux desktops run no firewall;
// Fedora (firewalld) and some Ubuntu installs (ufw) do and block the port.
// macOS asks on its own the first time kuro listens beyond loopback.
func Check(ctx context.Context, port int, _ string) (Status, error) {
	switch runtime.GOOS {
	case "linux":
		if active(ctx, "firewalld") {
			return Status{
				Firewall: "firewalld",
				Hint:     "firewalld is running and blocks other devices until kuro's port is opened.",
				Command:  fmt.Sprintf("sudo firewall-cmd --permanent --add-port=%d/tcp && sudo firewall-cmd --reload", port),
			}, nil
		}
		if active(ctx, "ufw") {
			return Status{
				Firewall: "ufw",
				Hint:     "ufw is running and blocks other devices until kuro's port is allowed.",
				Command:  fmt.Sprintf("sudo ufw allow %d/tcp", port),
			}, nil
		}
	case "darwin":
		out, _ := exec.CommandContext(ctx, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate").Output()
		if strings.Contains(string(out), "enabled") {
			return Status{
				Firewall: "macOS firewall",
				Hint: "macOS asks the first time kuro is opened to the network. If that was declined, " +
					"allow kuro in System Settings › Network › Firewall › Options.",
			}, nil
		}
	}
	return Status{}, nil
}

func active(ctx context.Context, unit string) bool {
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func Allow(context.Context, int, string, bool) error {
	return errors.New("kuro changes the firewall only on Windows; run the command shown instead")
}

func OpenNetworkSettings() error {
	return errors.New("only on Windows")
}
