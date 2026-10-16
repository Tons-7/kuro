<#
.SYNOPSIS
Downloads the external binaries kuro shells out to, into bin/.

.DESCRIPTION
kuro needs programs it does not bundle: ffmpeg and ffprobe for transcoding,
and mpv (with the Anime4K shaders) for external playback. This resolves the
current version of each from upstream, downloads it, and extracts only what
kuro uses.

Re-running is cheap: a component whose recorded version already matches is
skipped unless -Force is given. What was installed is recorded in
bin/versions.json so a build can be reproduced.

.PARAMETER Component
Fetch only some of ffmpeg, mpv, anime4k. Default is all.

.PARAMETER Force
Re-download even when the recorded version already matches.

.EXAMPLE
./scripts/fetch-deps.ps1
./scripts/fetch-deps.ps1 -Component mpv -Force
#>
[CmdletBinding()]
param(
    [ValidateSet('ffmpeg', 'mpv', 'anime4k')]
    [string[]]$Component = @('ffmpeg', 'mpv', 'anime4k'),

    [string]$BinDir,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Write-Progress makes downloads several times slower.

if (-not $BinDir) { $BinDir = Join-Path (Split-Path $PSScriptRoot -Parent) 'bin' }
$manifestPath = Join-Path $BinDir 'versions.json'

# TLS 1.2 isn't Windows PowerShell 5.1's default, and every source below refuses anything older.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$github = @{ 'User-Agent' = 'kuro-fetch-deps'; 'Accept' = 'application/vnd.github+json' }

function Write-Step($message) { Write-Host "==> $message" -ForegroundColor Cyan }
function Write-Note($message) { Write-Host "    $message" -ForegroundColor DarkGray }

function Get-Manifest {
    if (Test-Path $manifestPath) {
        try { return Get-Content $manifestPath -Raw | ConvertFrom-Json } catch { }
    }
    return [pscustomobject]@{}
}

function Save-Manifest($manifest) {
    # No BOM: Windows PowerShell's utf8 puts one first, and the Go side could not read the file at all.
    [IO.File]::WriteAllText($manifestPath, ($manifest | ConvertTo-Json -Depth 4))
}

# Checked against the checksum the source publishes, as the in-app installer checks the same downloads.
function Get-Download($url, $destination, $digest) {
    Write-Note "downloading $url"
    Invoke-WebRequest -Uri $url -OutFile $destination -UseBasicParsing -TimeoutSec 900
    $size = (Get-Item $destination).Length / 1MB
    Write-Note ("received {0:N1} MB" -f $size)
    if (-not $digest) {
        Write-Warning "no checksum published for $url; installing unverified"
        return
    }
    $got = (Get-FileHash $destination -Algorithm SHA256).Hash
    if ($got -ne $digest.ToUpper()) { throw "checksum mismatch for $url, refusing to install" }
    Write-Note 'checksum ok'
}

# The sha256 GitHub records for an asset, or nothing for releases older than that.
function Get-AssetDigest($asset) {
    if ($asset.digest -and $asset.digest -match '^sha256:([0-9a-fA-F]{64})$') { return $Matches[1] }
    return $null
}

# Windows ships bsdtar, which reads 7-Zip as well as zip. Requiring a separate
# 7-Zip install just to unpack mpv would defeat the point of this script.
function Expand-Download($archive, $destination) {
    New-Item -ItemType Directory -Force -Path $destination | Out-Null

    if ([IO.Path]::GetExtension($archive) -eq '.zip') {
        Expand-Archive -Path $archive -DestinationPath $destination -Force
        return
    }

    & tar.exe -xf $archive -C $destination
    if ($LASTEXITCODE -ne 0) {
        throw "tar could not extract $archive (exit $LASTEXITCODE). Windows 10 1803 or newer is required."
    }
}

# Copy-Item fails on a running executable, as when kuro is open; saying so beats an access-denied trace.
function Install-Binary($source, $name) {
    $target = Join-Path $BinDir $name
    try {
        Copy-Item -Path $source -Destination $target -Force
    } catch {
        throw "could not write $name - is kuro or $name still running? ($($_.Exception.Message))"
    }
    Write-Note ("installed {0} ({1:N1} MB)" -f $name, ((Get-Item $target).Length / 1MB))
}

function Find-One($root, $pattern) {
    $hit = Get-ChildItem -Path $root -Recurse -File -Filter $pattern | Select-Object -First 1
    if (-not $hit) { throw "$pattern not found in the downloaded archive" }
    return $hit.FullName
}

# gyan.dev tracks ffmpeg releases more closely than the GitHub build farms and
# publishes the version as a bare string, so no scraping is needed.
function Resolve-Ffmpeg {
    $raw = (Invoke-WebRequest 'https://www.gyan.dev/ffmpeg/builds/release-version' -UseBasicParsing -TimeoutSec 60).Content
    if ($raw -is [byte[]]) { $raw = [Text.Encoding]::UTF8.GetString($raw) }

    $url = 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-full.7z'
    $digest = $null
    try {
        $sum = (Invoke-WebRequest "$url.sha256" -UseBasicParsing -TimeoutSec 60).Content
        if ($sum -is [byte[]]) { $sum = [Text.Encoding]::UTF8.GetString($sum) }
        if ($sum -match '([0-9a-fA-F]{64})') { $digest = $Matches[1] }
    } catch { }
    return [pscustomobject]@{
        Version = $raw.Trim()
        Url     = $url
        Archive = $true
        Digest  = $digest
    }
}

# The plain x86_64 build runs anywhere; the -v3 variant needs AVX2 and the -dev
# archive is headers and import libraries rather than a player.
function Resolve-Mpv {
    $release = Invoke-RestMethod 'https://api.github.com/repos/shinchiro/mpv-winbuild-cmake/releases/latest' -Headers $github -TimeoutSec 60
    $asset = $release.assets |
        Where-Object { $_.name -match '^mpv-x86_64-\d{8}-git-[0-9a-f]+\.7z$' } |
        Select-Object -First 1
    if (-not $asset) { throw "no plain mpv x86_64 build in release $($release.tag_name)" }

    return [pscustomobject]@{
        Version = $asset.name -replace '^mpv-x86_64-' -replace '\.7z$'
        Url     = $asset.browser_download_url
        Archive = $true
        Digest  = Get-AssetDigest $asset
    }
}

# Upscaling shaders for mpv. Under a megabyte, and useless without mpv.
function Resolve-Anime4K {
    $release = Invoke-RestMethod 'https://api.github.com/repos/bloc97/Anime4K/releases/latest' -Headers $github -TimeoutSec 60
    $asset = $release.assets | Where-Object { $_.name -match '\.zip$' } | Select-Object -First 1
    if (-not $asset) { throw "no zip in Anime4K release $($release.tag_name)" }

    # Older than GitHub's digests: pinned, the release has not changed since 2021.
    $digest = Get-AssetDigest $asset
    if (-not $digest -and $release.tag_name -eq 'v4.0.1') { $digest = '139cd282086457c5adc79caf7b75b8b825091d71c9b54958c18745fea62d7ed7' }
    return [pscustomobject]@{
        Version = $release.tag_name.TrimStart('v')
        Url     = $asset.browser_download_url
        Archive = $true
        Digest  = $digest
    }
}

$components = [ordered]@{
    ffmpeg  = @{ Resolve = ${function:Resolve-Ffmpeg};  Files = @('ffmpeg.exe', 'ffprobe.exe') }
    mpv     = @{ Resolve = ${function:Resolve-Mpv};     Files = @('mpv.exe', 'mpv.com') }
    # mpv resolves the chain from bin/shaders, so the layout matters.
    anime4k = @{ Resolve = ${function:Resolve-Anime4K}; Glob = '*.glsl'; Into = 'shaders' }
}

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
$manifest = Get-Manifest
$staging = Join-Path ([IO.Path]::GetTempPath()) ("kuro-deps-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $staging | Out-Null

$summary = @()
try {
    foreach ($name in $Component) {
        $spec = $components[$name]
        Write-Step $name

        $resolved = & $spec.Resolve
        $installed = $manifest.$name

        if ($spec.Glob) {
            $into = Join-Path $BinDir $spec.Into
            $present = @((Test-Path $into) -and @(Get-ChildItem $into -Filter $spec.Glob -ErrorAction SilentlyContinue).Count -gt 0)
        } else {
            $present = $spec.Files | ForEach-Object { Test-Path (Join-Path $BinDir $_) }
        }

        if (-not $Force -and $installed -eq $resolved.Version -and $present -notcontains $false) {
            Write-Note "already at $($resolved.Version), skipping"
            $summary += [pscustomobject]@{ Component = $name; Version = $resolved.Version; Action = 'skipped' }
            continue
        }
        Write-Note "version $($resolved.Version)"

        if (-not $resolved.Archive) {
            $file = Join-Path $staging "$name.exe"
            Get-Download $resolved.Url $file $resolved.Digest
            Install-Binary $file $spec.Files[0]
        } else {
            $archive = Join-Path $staging ([IO.Path]::GetFileName(([Uri]$resolved.Url).AbsolutePath))
            Get-Download $resolved.Url $archive $resolved.Digest

            $unpacked = Join-Path $staging "$name-unpacked"
            Expand-Download $archive $unpacked

            if ($spec.Glob) {
                $into = Join-Path $BinDir $spec.Into
                New-Item -ItemType Directory -Force -Path $into | Out-Null
                $found = Get-ChildItem $unpacked -Recurse -File -Filter $spec.Glob
                if (-not $found) { throw "no $($spec.Glob) in the downloaded archive" }
                $found | ForEach-Object { Copy-Item $_.FullName (Join-Path $into $_.Name) -Force }
                Write-Note "installed $($found.Count) files into $($spec.Into)/"
            } else {
                foreach ($file in $spec.Files) {
                    Install-Binary (Find-One $unpacked $file) $file
                }
            }
        }

        $manifest | Add-Member -NotePropertyName $name -NotePropertyValue $resolved.Version -Force
        Save-Manifest $manifest
        $summary += [pscustomobject]@{ Component = $name; Version = $resolved.Version; Action = 'installed' }
    }
} finally {
    Remove-Item -Recurse -Force $staging -ErrorAction SilentlyContinue
}

Write-Host ''
$summary | Format-Table -AutoSize
Write-Host "bin: $BinDir" -ForegroundColor DarkGray
