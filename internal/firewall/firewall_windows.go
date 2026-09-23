package firewall

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
)

const createNoWindow = 0x08000000

func powershell(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "powershell", append([]string{"-NoProfile", "-NonInteractive"}, args...)...)
	// kuro has no console; without this every check flashes one.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

// encoded is what -EncodedCommand takes: UTF-16LE, base64. No quoting to get wrong.
func encoded(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

const statusScript = `$ErrorActionPreference = 'SilentlyContinue'
$nets = @(Get-NetConnectionProfile | ForEach-Object {
  [pscustomobject]@{ name = $_.Name; interface = $_.InterfaceAlias; category = $_.NetworkCategory.ToString() } })
function Shape($r) { [pscustomobject]@{ name = $r.DisplayName; action = $r.Action.ToString(); profiles = $r.Profile.ToString(); enabled = ($r.Enabled.ToString() -eq 'True') } }
$port = @(Get-NetFirewallRule -DisplayName %s | ForEach-Object { Shape $_ })
$prog = @(Get-NetFirewallApplicationFilter -Program %s | Get-NetFirewallRule | Where-Object { $_.Direction.ToString() -eq 'Inbound' } | ForEach-Object { Shape $_ })
[pscustomobject]@{ networks = $nets; port = $port; program = $prog } | ConvertTo-Json -Depth 4 -Compress`

// Check reads the network categories and every rule that decides whether a
// device on them reaches the port.
func Check(ctx context.Context, port int, exe string) (Status, error) {
	out, err := powershell(ctx, "-EncodedCommand",
		encoded(fmt.Sprintf(statusScript, quote(RuleName(port)), quote(exe)))).Output()
	if err != nil {
		return Status{Supported: true}, fmt.Errorf("read firewall: %w", err)
	}
	var raw struct {
		Networks []Network `json:"networks"`
		Port     []Rule    `json:"port"`
		Program  []Rule    `json:"program"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Status{Supported: true}, fmt.Errorf("read firewall: %w", err)
	}
	st := Status{Supported: true, Networks: raw.Networks, Program: raw.Program}
	if len(raw.Port) > 0 {
		st.PortRule = &raw.Port[0]
	}
	return st, nil
}

const allowScript = `$ErrorActionPreference = 'Stop'
Remove-NetFirewallRule -DisplayName %[1]s -ErrorAction SilentlyContinue
New-NetFirewallRule -DisplayName %[1]s -Description 'Lets devices on this network open kuro.' -Direction Inbound -Action Allow -Protocol TCP -LocalPort %[2]d -RemoteAddress LocalSubnet -Profile %[3]s | Out-Null
Get-NetFirewallApplicationFilter -Program %[4]s -ErrorAction SilentlyContinue | Get-NetFirewallRule | Where-Object { $_.Direction.ToString() -eq 'Inbound' -and $_.Action.ToString() -eq 'Block' } | Remove-NetFirewallRule`

// Allow adds kuro's port rule for the local subnet and removes the block rules
// a declined prompt left on kuro.exe, which would override it. Windows asks the
// user to approve, since this needs administrator rights.
func Allow(ctx context.Context, port int, exe string, public bool) error {
	profiles := "Private,Domain"
	if public {
		profiles = "Private,Domain,Public"
	}
	script := fmt.Sprintf(allowScript, quote(RuleName(port)), port, profiles, quote(exe))
	// The elevated child cannot report back directly; its exit code can.
	launcher := fmt.Sprintf(`try { $p = Start-Process powershell -Verb RunAs -Wait -PassThru -WindowStyle Hidden -ArgumentList '-NoProfile','-NonInteractive','-EncodedCommand','%s'; exit $p.ExitCode } catch { exit 1223 }`,
		encoded(script))
	err := powershell(ctx, "-Command", launcher).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1223 {
		return ErrCancelled
	}
	if err != nil {
		return fmt.Errorf("add firewall rule: %w", err)
	}
	return nil
}

// OpenNetworkSettings shows where a network is switched from Public to Private.
func OpenNetworkSettings() error {
	cmd := exec.Command("explorer", "ms-settings:network-status")
	return cmd.Start()
}
