<#
.SYNOPSIS
Checks release matching across a spread of anime rather than one episode.

.DESCRIPTION
Searches for one episode of each sampled show and reports whether anything was
found, whether a release was good enough to auto-pick, and why candidates were
refused. Nothing is downloaded.
#>
param(
    [int]$Count = 15,
    [string]$Base = "http://127.0.0.1:4321"
)

$ErrorActionPreference = 'Continue'

function Sample($sort, $n) {
    try {
        (Invoke-RestMethod "$Base/api/discover?sort=$sort&perPage=$n" -TimeoutSec 120).items
    } catch { @() }
}

$pool = @()
foreach ($s in 'trending', 'popular', 'season') { $pool += Sample $s 20 }
$pool = $pool | Where-Object { $_.id } | Sort-Object id -Unique | Get-Random -Count $Count

$rows = @()
foreach ($a in $pool) {
    $ep = 1
    if ($a.progress -gt 0) { $ep = [int]$a.progress }

    $r = $null
    try {
        $r = Invoke-RestMethod "$Base/api/episode/sources?id=$($a.id)&episode=$ep" -TimeoutSec 300
    } catch {
        $rows += [pscustomobject]@{
            Title = $a.title; Ep = $ep; Found = 0; Pick = 'ERROR'; Auto = 0; Why = $_.Exception.Message
        }
        continue
    }

    $found = @($r.results).Count
    $auto = @($r.results | Where-Object { $_.autoPick }).Count
    $why = ''
    if (-not $r.best) {
        $why = (@($r.results | Where-Object { $_.rejections } |
                ForEach-Object { $_.rejections[0].rule }) | Group-Object |
                Sort-Object Count -Descending | Select-Object -First 2 |
                ForEach-Object { "$($_.Name)x$($_.Count)" }) -join ' '
        if (-not $why) { $why = 'no candidates' }
    }

    $rows += [pscustomobject]@{
        Title = $a.title.Substring(0, [Math]::Min(38, $a.title.Length))
        Ep    = $ep
        Found = $found
        Pick  = $(if ($r.best) { 'yes' } else { 'NO' })
        Auto  = $auto
        Why   = $why
    }
    Start-Sleep -Milliseconds 400
}

$rows | Format-Table -AutoSize | Out-String -Width 160
$ok = @($rows | Where-Object { $_.Pick -eq 'yes' }).Count
"playable: $ok / $($rows.Count)"
