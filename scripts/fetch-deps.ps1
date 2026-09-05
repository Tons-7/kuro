<#
.SYNOPSIS
Downloads the external binaries kuro shells out to, into bin/.

.DESCRIPTION
kuro needs three programs it does not bundle: rqbit for torrents, ffmpeg and
ffprobe for transcoding, and mpv for external playback. This resolves the
current version of each from upstream, downloads it, and extracts only the
executables.

Re-running is cheap: a component whose recorded version already matches is
skipped unless -Force is given. What was installed is recorded in
bin/versions.json so a build can be reproduced.

.PARAMETER Component
Fetch only one of rqbit, ffmpeg, mpv. Default is all three.

.PARAMETER Force
Re-download even when the recorded version already matches.

.EXAMPLE
./scripts/fetch-deps.ps1
./scripts/fetch-deps.ps1 -Component mpv -Force
#>
[CmdletBinding()]
param(
    [ValidateSet('rqbit', 'ffmpeg', 'mpv', 'anime4k')]
    [string[]]$Component = @('rqbit', 'ffmpeg', 'mpv', 'anime4k'),

    [string]$BinDir,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Write-Progress makes downloads several times slower.

if (-not $BinDir) { $BinDir = Join-Path (Split-Path $PSScriptRoot -Parent) 'bin' }
$manifestPath = Join-Path $BinDir 'versions.json'

# TLS 1.2 is not the default in Windows PowerShell 5.1 and every source below
# refuses anything older.
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
    $manifest | ConvertTo-Json -Depth 4 | Set-Content $manifestPath -Encoding utf8
}

function Get-Download($url, $destination) {
    Write-Note "downloading $url"
    Invoke-WebRequest -Uri $url -OutFile $destination -UseBasicParsing -TimeoutSec 900
    $size = (Get-Item $destination).Length / 1MB
    Write-Note ("received {0:N1} MB" -f $size)
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

# Copy-Item on a running executable fails, which is exactly what happens when
# kuro is open. Saying so beats an access-denied trace.
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

# rqbit tags its betas as full releases, so the prerelease flag cannot be
# trusted; a plain semver tag is the only reliable marker of a stable build.
function Resolve-Rqbit {
    $releases = Invoke-RestMethod 'https://api.github.com/repos/ikatson/rqbit/releases?per_page=40' -Headers $github -TimeoutSec 60
    $stable = $releases | Where-Object { $_.tag_name -match '^v\d+\.\d+\.\d+$' } | Select-Object -First 1
    if (-not $stable) { throw 'no stable rqbit release found' }

    $asset = $stable.assets | Where-Object { $_.name -eq 'rqbit.exe' } | Select-Object -First 1
    if (-not $asset) { throw "rqbit $($stable.tag_name) has no Windows build" }

    return [pscustomobject]@{
        Version = $stable.tag_name.TrimStart('v')
        Url     = $asset.browser_download_url
        Archive = $false
    }
}

# gyan.dev tracks ffmpeg releases more closely than the GitHub build farms and
# publishes the version as a bare string, so no scraping is needed.
function Resolve-Ffmpeg {
    $raw = (Invoke-WebRequest 'https://www.gyan.dev/ffmpeg/builds/release-version' -UseBasicParsing -TimeoutSec 60).Content
    if ($raw -is [byte[]]) { $raw = [Text.Encoding]::UTF8.GetString($raw) }

    return [pscustomobject]@{
        Version = $raw.Trim()
        Url     = 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-full.7z'
        Archive = $true
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
    }
}

# Upscaling shaders for mpv. Under a megabyte, and useless without mpv.
function Resolve-Anime4K {
    $release = Invoke-RestMethod 'https://api.github.com/repos/bloc97/Anime4K/releases/latest' -Headers $github -TimeoutSec 60
    $asset = $release.assets | Where-Object { $_.name -match '\.zip$' } | Select-Object -First 1
    if (-not $asset) { throw "no zip in Anime4K release $($release.tag_name)" }

    return [pscustomobject]@{
        Version = $release.tag_name.TrimStart('v')
        Url     = $asset.browser_download_url
        Archive = $true
    }
}

$components = [ordered]@{
    rqbit   = @{ Resolve = ${function:Resolve-Rqbit};   Files = @('rqbit.exe') }
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
            Get-Download $resolved.Url $file
            Install-Binary $file $spec.Files[0]
        } else {
            $archive = Join-Path $staging ([IO.Path]::GetFileName(([Uri]$resolved.Url).AbsolutePath))
            Get-Download $resolved.Url $archive

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
