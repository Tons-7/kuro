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

# Under Stop, stderr output becomes a terminating error; exit codes say whether a step failed.
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
        if ($LASTEXITCODE -ne 0) { Fail 'type check failed' }
        npx vite build
        if ($LASTEXITCODE -ne 0) { Fail 'frontend build failed' }
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
    # The name from its parts: a regex on 'kuro.exe' matched twice and gave kuro-windows-arm64.exe-windows-arm64.
    $targetOS = if ($goos) { $goos } else { go env GOOS }
    $ext = if ($targetOS -eq 'windows') { '.exe' } else { '' }
    $suffix = if ($Target) { "-$goos-$goarch" } else { '' }
    $Out = Join-Path $root "kuro$suffix$ext"
}
# go -C resolves a relative -o against the repo, not where this was run from; say the same path it will use.
$Out = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Out)

# Set for the build and put back after: the caller's shell keeps its own values.
$was = @{ CGO_ENABLED = $env:CGO_ENABLED; GOOS = $env:GOOS; GOARCH = $env:GOARCH }
$env:CGO_ENABLED = '0'
if ($goos) { $env:GOOS = $goos; $env:GOARCH = $goarch }
try {
    # Trimming the symbol table roughly halves the binary; nothing needs a Go stack trace in production.
    $ldflags = '-s -w'
    if ($Version) { $ldflags += " -X kuro/internal/update.Version=$Version" }
    go -C $root build -trimpath -ldflags $ldflags -o $Out ./cmd/kuro
    if ($LASTEXITCODE -ne 0) { Fail 'go build failed' }
} finally {
    foreach ($name in $was.Keys) {
        if ($null -eq $was[$name]) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue } else { Set-Item "Env:$name" $was[$name] }
    }
}

$size = [math]::Round((Get-Item $Out).Length / 1MB, 1)
Write-Host "built $Out ($size MB)" -ForegroundColor Green
