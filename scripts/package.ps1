<#
.SYNOPSIS
Builds the zips that get sent to someone else.

.DESCRIPTION
Two artifacts, because installing and updating want different things:

  kuro-<version>.zip         kuro.exe and a README. First install.
  kuro-<version>-update.zip  kuro.exe alone. What the in-app updater fetches.
  SHA256SUMS.txt             The updater refuses a zip that does not match.

Neither carries bin/, cache/ or config.toml. bin/ is over half a gigabyte and
the app downloads it itself on first run; cache/ is downloaded episodes; and
config.toml holds the AniList client secret, which must never be shipped.

Updating means replacing kuro.exe, not the folder — the folder is where the
sidecars, the episode cache and the credentials live. The database is in
%LOCALAPPDATA%\kuro and is migrated on startup, so watch history survives
whatever happens to the folder.

.PARAMETER Version
Stamped into the binary and the file names. Defaults to the date; a second
build the same day needs a suffix (2026.08.27.1) or installed copies will not
see it as newer.

.PARAMETER Publish
Creates the GitHub release (tag v<version>) with the zips attached, which is
what every installed copy checks. Needs `gh auth login` once.

.EXAMPLE
./scripts/package.ps1
./scripts/package.ps1 -Version 2026.08.27 -Publish
#>
[CmdletBinding()]
param(
    [string]$Version,
    [string]$OutDir,
    [switch]$Publish,
    [string]$Notes
)

$ErrorActionPreference = 'Stop'

$root = Split-Path $PSScriptRoot -Parent
if (-not $Version) { $Version = Get-Date -Format 'yyyy.MM.dd' }
if (-not $OutDir) { $OutDir = Join-Path $root 'dist' }

function Write-Step($message) { Write-Host "==> $message" -ForegroundColor Cyan }
function Write-Note($message) { Write-Host "    $message" -ForegroundColor DarkGray }

Write-Step "build $Version"
& (Join-Path $PSScriptRoot 'build.ps1') -Version $Version
if ($LASTEXITCODE -ne 0) { throw 'build failed' }

$exe = Join-Path $root 'kuro.exe'
if (-not (Test-Path $exe)) { throw "kuro.exe not found at $exe" }

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$staging = Join-Path ([IO.Path]::GetTempPath()) ("kuro-package-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $staging | Out-Null

$readme = @"
kuro $Version

Run kuro.exe. It opens its own window and, on first run, asks to download the
programs it needs — a torrent engine and ffmpeg, about 430 MB. Nothing is
installed anywhere else: everything lives beside kuro.exe.

kuro ships with no torrent sites. The setup screen shows the block to add to
config.toml (written beside kuro.exe on first run); add your sites and restart.

Windows will warn that the app is unsigned. More info -> Run anyway.

Your watch history, library and settings are kept in:
    %LOCALAPPDATA%\kuro

Updates: kuro checks for a new version on launch and offers it under
Settings -> About. Only kuro.exe is replaced; the downloaded programs, the
episode cache and your settings stay. To update by hand, replace kuro.exe —
never the whole folder.

Connecting an AniList account is optional and needs a client id of your own;
config.toml, written on first run, says how.
"@

try {
    Write-Step 'full'
    $full = Join-Path $staging 'full'
    New-Item -ItemType Directory -Force -Path $full | Out-Null
    Copy-Item $exe (Join-Path $full 'kuro.exe')
    Set-Content -Path (Join-Path $full 'README.txt') -Value $readme -Encoding utf8

    $fullZip = Join-Path $OutDir "kuro-$Version.zip"
    Remove-Item $fullZip -ErrorAction SilentlyContinue
    Compress-Archive -Path (Join-Path $full '*') -DestinationPath $fullZip
    Write-Note ("{0} ({1:N1} MB)" -f (Split-Path $fullZip -Leaf), ((Get-Item $fullZip).Length / 1MB))

    Write-Step 'update'
    $updateZip = Join-Path $OutDir "kuro-$Version-update.zip"
    Remove-Item $updateZip -ErrorAction SilentlyContinue
    Compress-Archive -Path $exe -DestinationPath $updateZip
    Write-Note ("{0} ({1:N1} MB)" -f (Split-Path $updateZip -Leaf), ((Get-Item $updateZip).Length / 1MB))

    Write-Step 'checksums'
    $sums = Join-Path $OutDir 'SHA256SUMS.txt'
    $lines = foreach ($zip in @($fullZip, $updateZip)) {
        "{0}  {1}" -f (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower(), (Split-Path $zip -Leaf)
    }
    # LF and no BOM: the updater reads it byte for byte.
    [IO.File]::WriteAllText($sums, ($lines -join "`n") + "`n")
    Write-Note (Split-Path $sums -Leaf)
} finally {
    Remove-Item -Recurse -Force $staging -ErrorAction SilentlyContinue
}

# A secret shipped by accident cannot be unshipped, so say plainly what went in.
Write-Host ''
Get-ChildItem $OutDir -Filter "kuro-$Version*.zip" | ForEach-Object {
    Write-Host "  $($_.Name)" -ForegroundColor Green
    Expand-Archive -Path $_.FullName -DestinationPath (Join-Path $staging 'verify') -Force
}
Get-ChildItem (Join-Path $staging 'verify') -Recurse -File -ErrorAction SilentlyContinue |
    ForEach-Object { Write-Host "      $($_.Name)" -ForegroundColor DarkGray }
Remove-Item -Recurse -Force (Join-Path $staging 'verify') -ErrorAction SilentlyContinue

Write-Host ''
Write-Host "out: $OutDir" -ForegroundColor DarkGray

if ($Publish) {
    Write-Step "publish v$Version"
    if (-not $Notes) { $Notes = "kuro $Version" }
    gh release create "v$Version" $fullZip $updateZip $sums `
        --repo Tons-7/kuro --title "kuro $Version" --notes $Notes
    if ($LASTEXITCODE -ne 0) { throw 'gh release create failed' }
    Write-Host "published https://github.com/Tons-7/kuro/releases/tag/v$Version" -ForegroundColor Green
}
