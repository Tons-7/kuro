<#
.SYNOPSIS
Builds the zips that get sent to someone else.

.DESCRIPTION
Two artifacts, because installing and updating want different things:

  kuro-<version>.zip             kuro.exe and a README. First install.
  kuro-<version>-update.zip      kuro.exe alone. What the in-app updater fetches.
  kuro-<version>-<os>-<arch>.zip The macOS and Linux binaries, which their updaters fetch.
  SHA256SUMS.txt                 The updater refuses a zip that does not match.
  SHA256SUMS.txt.sig             Its ed25519 signature; the updater refuses a release without one.

Neither carries bin/, cache/ or config.toml. bin/ is over half a gigabyte and
the app downloads it itself on first run; cache/ is downloaded episodes; and
config.toml holds the AniList client secret, which must never be shipped.

Updating means replacing kuro.exe, not the folder — the folder is where the
downloaded programs, the episode cache and the credentials live. The database is in
%LOCALAPPDATA%\kuro and is migrated on startup, so watch history survives
whatever happens to the folder.

.PARAMETER Version
Stamped into the binary and the file names. Defaults to the date; a second
build the same day needs a suffix (2026.08.27.1) or installed copies will not
see it as newer.

.PARAMETER Publish
Creates the GitHub release (tag v<version>) with the zips attached, which is
what every installed copy checks. Needs `gh auth login` once.

.PARAMETER SigningKey
The release key's file (a hex ed25519 seed), kept outside the repository.
Default: %LOCALAPPDATA%\kuro-release\update-signing.key. Back it up: without it
no installed copy will accept another update.

.EXAMPLE
./scripts/package.ps1
./scripts/package.ps1 -Version 2026.08.27 -Publish
#>
[CmdletBinding()]
param(
    [string]$Version,
    [string]$OutDir,
    [switch]$Publish,
    [string]$Notes,
    [string]$SigningKey
)

$ErrorActionPreference = 'Stop'

$root = Split-Path $PSScriptRoot -Parent
if (-not $Version) { $Version = Get-Date -Format 'yyyy.MM.dd' }
if (-not $OutDir) { $OutDir = Join-Path $root 'dist' }
if (-not $SigningKey) { $SigningKey = Join-Path $env:LOCALAPPDATA 'kuro-release\update-signing.key' }
if (-not (Test-Path $SigningKey)) { throw "release key not found at $SigningKey; every installed copy refuses an unsigned release" }

# The platforms build.sh users run; their updaters look for these zips by name.
$platforms = @('darwin/arm64', 'darwin/amd64', 'linux/amd64', 'linux/arm64')

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

Extract this zip to a folder of its own first. Run kuro.exe from there, never
from inside the zip: Windows would put it in a temporary folder and throw your
settings away every time it closes.

kuro.exe opens its own window and, on first run, asks to download ffmpeg, about
420 MB (the torrent engine is built in; an ffmpeg already on PATH is used as is).
Nothing is installed anywhere else: everything lives beside kuro.exe.

kuro ships with no torrent sites. The setup screen shows the block to add to
config.toml (written beside kuro.exe on first run); add your sites and restart.

Windows will warn that the app is unsigned. More info -> Run anyway.

Your watch history, library and settings are kept in:
    %LOCALAPPDATA%\kuro
To keep everything in this folder instead, pick a folder under Setup ->
Where things go.

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
    # The license's Required Notice must travel with every copy.
    Copy-Item (Join-Path $root 'LICENSE.md') (Join-Path $full 'LICENSE.md')

    $fullZip = Join-Path $OutDir "kuro-$Version.zip"
    Remove-Item $fullZip -ErrorAction SilentlyContinue
    Compress-Archive -Path (Join-Path $full '*') -DestinationPath $fullZip
    Write-Note ("{0} ({1:N1} MB)" -f (Split-Path $fullZip -Leaf), ((Get-Item $fullZip).Length / 1MB))

    Write-Step 'update'
    $updateZip = Join-Path $OutDir "kuro-$Version-update.zip"
    Remove-Item $updateZip -ErrorAction SilentlyContinue
    Compress-Archive -Path $exe -DestinationPath $updateZip
    Write-Note ("{0} ({1:N1} MB)" -f (Split-Path $updateZip -Leaf), ((Get-Item $updateZip).Length / 1MB))

    $platformZips = @()
    foreach ($platform in $platforms) {
        $goos, $goarch = $platform -split '/'
        Write-Step "$goos/$goarch"
        $dir = Join-Path $staging "$goos-$goarch"
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        & (Join-Path $PSScriptRoot 'build.ps1') -SkipWeb -Target $platform -Version $Version -Out (Join-Path $dir 'kuro')
        if ($LASTEXITCODE -ne 0) { throw "build for $platform failed" }
        Set-Content -Path (Join-Path $dir 'README.txt') -Value $readme -Encoding utf8
        Copy-Item (Join-Path $root 'LICENSE.md') (Join-Path $dir 'LICENSE.md')
        $zip = Join-Path $OutDir "kuro-$Version-$goos-$goarch.zip"
        Remove-Item $zip -ErrorAction SilentlyContinue
        Compress-Archive -Path (Join-Path $dir '*') -DestinationPath $zip
        Write-Note ("{0} ({1:N1} MB)" -f (Split-Path $zip -Leaf), ((Get-Item $zip).Length / 1MB))
        $platformZips += $zip
    }

    Write-Step 'checksums'
    $sums = Join-Path $OutDir 'SHA256SUMS.txt'
    $lines = foreach ($zip in @($fullZip, $updateZip) + $platformZips) {
        "{0}  {1}" -f (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower(), (Split-Path $zip -Leaf)
    }
    # LF and no BOM: the updater reads it byte for byte.
    [IO.File]::WriteAllText($sums, ($lines -join "`n") + "`n")
    Write-Note (Split-Path $sums -Leaf)

    Write-Step 'sign'
    $sig = "$sums.sig"
    go -C $root run ./scripts/signsums -key $SigningKey -sums $sums -out $sig
    if ($LASTEXITCODE -ne 0) { throw 'signing failed' }
} finally {
    Remove-Item -Recurse -Force $staging -ErrorAction SilentlyContinue
}

# A secret shipped by accident can't be unshipped: stop on any zip entry but the program, README, license.
Add-Type -AssemblyName System.IO.Compression.FileSystem
Write-Host ''
$allowed = @('kuro.exe', 'kuro', 'README.txt', 'LICENSE.md')
foreach ($zip in @($fullZip, $updateZip) + $platformZips) {
    Write-Host "  $(Split-Path $zip -Leaf)" -ForegroundColor Green
    $archive = [IO.Compression.ZipFile]::OpenRead($zip)
    try {
        foreach ($entry in $archive.Entries) {
            Write-Host "      $($entry.FullName)" -ForegroundColor DarkGray
            if ($allowed -notcontains $entry.FullName) { throw "$(Split-Path $zip -Leaf) holds $($entry.FullName), which must not ship" }
        }
    } finally {
        $archive.Dispose()
    }
}

Write-Host ''
Write-Host "out: $OutDir" -ForegroundColor DarkGray

if ($Publish) {
    Write-Step "publish v$Version"
    if (-not $Notes) { $Notes = "kuro $Version" }
    gh release create "v$Version" $fullZip $updateZip @platformZips $sums $sig `
        --repo Tons-7/kuro --title "kuro $Version" --notes $Notes
    if ($LASTEXITCODE -ne 0) { throw 'gh release create failed' }
    Write-Host "published https://github.com/Tons-7/kuro/releases/tag/v$Version" -ForegroundColor Green
}
