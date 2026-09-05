<#
.SYNOPSIS
Builds the frontend and then the binary that embeds it.

.DESCRIPTION
The single-page app is compiled into web/dist and embedded by web/embed.go, so
the frontend must be built first or the binary serves a placeholder. This does
both in the right order.

.PARAMETER Target
Cross-compile for another platform, as GOOS/GOARCH, e.g. linux/amd64.

.PARAMETER SkipWeb
Reuse the existing web/dist, for when only Go code changed.

.PARAMETER Version
Stamped into the binary; the updater compares it against the latest release.
Unstamped builds are "dev" and never update themselves.
#>
[CmdletBinding()]
param(
    [string]$Target,
    [switch]$SkipWeb,
    [string]$Out,
    [string]$Version
)

# Native tools write progress and warnings to stderr, which Windows PowerShell
# turns into terminating errors under Stop. Exit codes are what actually say
# whether a step failed, and every step below checks one.
$ErrorActionPreference = 'Continue'
$root = Split-Path $PSScriptRoot -Parent

function Fail($message) {
    Write-Host $message -ForegroundColor Red
    exit 1
}

if (-not $SkipWeb) {
    Write-Host '==> frontend' -ForegroundColor Cyan
    Push-Location (Join-Path $root 'web')
    try {
        if (-not (Test-Path 'node_modules')) { npm install }
        # -b, not --noEmit: the root tsconfig is a solution file with no files
        # of its own, so --noEmit checked nothing and exited 0 on anything.
        npx tsgo -b --force
        if ($LASTEXITCODE -ne 0) { Pop-Location; Fail 'type check failed' }
        npx vite build
        if ($LASTEXITCODE -ne 0) { Pop-Location; Fail 'frontend build failed' }
    } finally {
        Pop-Location
    }
}

Write-Host '==> binary' -ForegroundColor Cyan

$goos, $goarch = $null, $null
if ($Target) {
    $parts = $Target -split '/'
    if ($parts.Count -ne 2) { Fail "Target must look like linux/amd64, got $Target" }
    $goos, $goarch = $parts
}

if (-not $Out) {
    $name = if ($goos -eq 'windows' -or (-not $goos)) { 'kuro.exe' } else { 'kuro' }
    $suffix = if ($Target) { "-$goos-$goarch" } else { '' }
    $Out = Join-Path $root ($name -replace '(\.exe)?$', "$suffix`$1")
}

$env:CGO_ENABLED = '0'
if ($goos) { $env:GOOS = $goos; $env:GOARCH = $goarch }
try {
    # Trimming the symbol table roughly halves the binary; nothing here needs
    # a Go stack trace in production.
    $ldflags = '-s -w'
    if ($Version) { $ldflags += " -X kuro/internal/update.Version=$Version" }
    go -C $root build -trimpath -ldflags $ldflags -o $Out ./cmd/kuro
    if ($LASTEXITCODE -ne 0) { Fail 'go build failed' }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}

$size = [math]::Round((Get-Item $Out).Length / 1MB, 1)
Write-Host "built $Out ($size MB)" -ForegroundColor Green
