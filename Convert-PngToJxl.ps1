#requires -Version 5.1
<#
.SYNOPSIS
  Convert PNGs in this folder to JPEG XL one-at-a-time-per-worker, then delete
  the original only after the .jxl verifies. File times are copied across.

.EXAMPLE
  # Preview 12 files, keep the PNGs, judge quality in Explorer:
  powershell -ExecutionPolicy Bypass -File .\Convert-PngToJxl.ps1 -Limit 12 -KeepOriginals

.EXAMPLE
  # Full run (default: visually lossless, effort 7):
  powershell -ExecutionPolicy Bypass -File .\Convert-PngToJxl.ps1

.EXAMPLE
  # Pixel-perfect instead of visually lossless (larger files):
  powershell -ExecutionPolicy Bypass -File .\Convert-PngToJxl.ps1 -Lossless -Effort 7
#>
[CmdletBinding()]
param(
    [string]$Path,

    # cjxl effort. 7 matches cjxl's default; 10 is much slower and not smaller here.
    [ValidateRange(1, 10)]
    [int]$Effort = 7,

    # Butteraugli distance. Ignored when -Lossless is set. 1.0 is cjxl's PNG default.
    [ValidateRange(0.0, 25.0)]
    [double]$Distance = 1.0,

    [switch]$Lossless,

    # Parallel file encodes. cjxl is itself threaded; keep workers * threads ~= cores.
    [ValidateRange(1, 32)]
    [int]$Workers = 4,

    [ValidateRange(0, 64)]
    [int]$ThreadsPerWorker = 6,

    # Do not delete the PNG after a successful convert (for quality checks).
    [switch]$KeepOriginals,

    # Convert at most this many still-pending PNGs. 0 = no limit.
    [int]$Limit = 0,

    # Skip PNGs written this recently so an in-progress screenshot is not touched.
    [int]$SkipNewerThanSeconds = 30
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($Path)) {
    $scriptDir = $PSScriptRoot
    if (-not $scriptDir -and $MyInvocation.MyCommand.Path) {
        $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
    }
    if ($scriptDir) {
        $Path = Split-Path -Parent $scriptDir
    } else {
        $Path = (Get-Location).Path
    }
}

function Get-RequiredCommand {
    param([string]$Name)
    $cmd = Get-Command $Name -ErrorAction SilentlyContinue
    if (-not $cmd) {
        throw "Required command '$Name' is not on PATH."
    }
    return $cmd.Source
}

function Test-ValidJxl {
    param(
        [string]$JxlPath,
        [string]$Jxlinfo
    )
    if (-not (Test-Path -LiteralPath $JxlPath)) { return $false }
    if ((Get-Item -LiteralPath $JxlPath).Length -le 32) { return $false }
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $null = & $Jxlinfo $JxlPath 2>&1
        return ($LASTEXITCODE -eq 0)
    } catch {
        return $false
    } finally {
        $ErrorActionPreference = $prev
    }
}

function Copy-FileTimes {
    param(
        [string]$FromPath,
        [string]$ToPath
    )
    $from = Get-Item -LiteralPath $FromPath
    $to = Get-Item -LiteralPath $ToPath
    $to.CreationTimeUtc = $from.CreationTimeUtc
    $to.LastWriteTimeUtc = $from.LastWriteTimeUtc
}

$cjxl = Get-RequiredCommand 'cjxl'
$jxlinfo = Get-RequiredCommand 'jxlinfo'
$folder = (Resolve-Path -LiteralPath $Path).Path
$logPath = Join-Path $folder 'convert-png-to-jxl.log'
$effectiveDistance = if ($Lossless) { 0.0 } else { $Distance }

# Leftover temp files from a previous interrupted run.
Get-ChildItem -LiteralPath $folder -Filter '*.jxl.partial' -File -ErrorAction SilentlyContinue |
    ForEach-Object { Remove-Item -LiteralPath $_.FullName -Force }

$pending = @(
    Get-ChildItem -LiteralPath $folder -Filter '*.png' -File |
        Where-Object { $_.Length -gt 0 } |
        Sort-Object Name
)

$emptyCount = @(
    Get-ChildItem -LiteralPath $folder -Filter '*.png' -File |
        Where-Object { $_.Length -eq 0 }
).Count

$alreadyJxl = 0
$work = New-Object System.Collections.Generic.List[string]
foreach ($png in $pending) {
    $jxlPath = [IO.Path]::ChangeExtension($png.FullName, '.jxl')
    if ((Test-Path -LiteralPath $jxlPath) -and (Test-ValidJxl -JxlPath $jxlPath -Jxlinfo $jxlinfo)) {
        Copy-FileTimes -FromPath $png.FullName -ToPath $jxlPath
        if (-not $KeepOriginals) {
            Remove-Item -LiteralPath $png.FullName -Force
        }
        $alreadyJxl++
        continue
    }
    $work.Add($png.FullName)
}

if ($Limit -gt 0 -and $work.Count -gt $Limit) {
    $work = $work.GetRange(0, $Limit)
}

$queue = New-Object 'System.Collections.Concurrent.ConcurrentQueue[string]'
foreach ($item in $work) { $queue.Enqueue($item) }

$sync = [hashtable]::Synchronized(@{
        Converted = 0
        Failed    = 0
        Skipped   = 0
        BytesIn   = [int64]0
        BytesOut  = [int64]0
    })

$mode = if ($Lossless) { "lossless -d 0 -e $Effort" } else { "visually-lossy -d $effectiveDistance -e $Effort" }
$header = @(
    "=== $($(Get-Date).ToString('s')) convert PNG -> JXL ==="
    "folder=$folder"
    "mode=$mode workers=$Workers threads/worker=$ThreadsPerWorker keepOriginals=$KeepOriginals"
    "pending=$($work.Count) alreadyHadJxl=$alreadyJxl emptyPngsLeftAlone=$emptyCount"
)
$header | ForEach-Object { $_ ; Add-Content -LiteralPath $logPath -Value $_ }

if ($work.Count -eq 0) {
    Write-Host 'Nothing to convert.'
    exit 0
}

$workerScript = {
    param(
        $Queue,
        $Sync,
        $Cjxl,
        $Jxlinfo,
        $Effort,
        $Distance,
        $Threads,
        $KeepOriginals,
        $SkipNewerThanSeconds,
        $LogPath
    )

    $ErrorActionPreference = 'Continue'

    function Test-ValidJxlInner {
        param([string]$JxlPath, [string]$Tool)
        if (-not (Test-Path -LiteralPath $JxlPath)) { return $false }
        if ((Get-Item -LiteralPath $JxlPath).Length -le 32) { return $false }
        $null = & $Tool $JxlPath 2>&1
        return ($LASTEXITCODE -eq 0)
    }

    function Write-LogLine {
        param([string]$Line)
        $mutex = New-Object System.Threading.Mutex($false, 'Local\ConvertPngToJxlLog')
        try {
            $null = $mutex.WaitOne()
            Add-Content -LiteralPath $LogPath -Value $Line
        } finally {
            $mutex.ReleaseMutex()
            $mutex.Dispose()
        }
    }

    function Update-Stats {
        param(
            [string]$Field,
            [int64]$BytesIn = 0,
            [int64]$BytesOut = 0
        )
        [System.Threading.Monitor]::Enter($Sync.SyncRoot)
        try {
            $Sync[$Field] = [int]$Sync[$Field] + 1
            $Sync['BytesIn'] = [int64]$Sync['BytesIn'] + $BytesIn
            $Sync['BytesOut'] = [int64]$Sync['BytesOut'] + $BytesOut
        } finally {
            [System.Threading.Monitor]::Exit($Sync.SyncRoot)
        }
    }

    $pngPath = $null
    while ($Queue.TryDequeue([ref]$pngPath)) {
        try {
            $png = Get-Item -LiteralPath $pngPath
            $ageSec = ([DateTime]::UtcNow - $png.LastWriteTimeUtc).TotalSeconds
            if ($ageSec -lt $SkipNewerThanSeconds) {
                Update-Stats -Field 'Skipped'
                Write-LogLine "SKIP-RECENT`t$($png.Name)"
                continue
            }

            $dest = [IO.Path]::ChangeExtension($png.FullName, '.jxl')
            $partial = "$dest.partial"
            if (Test-Path -LiteralPath $partial) {
                Remove-Item -LiteralPath $partial -Force
            }

            if ((Test-Path -LiteralPath $dest) -and (Test-ValidJxlInner -JxlPath $dest -Tool $Jxlinfo)) {
                $out = Get-Item -LiteralPath $dest
                $out.CreationTimeUtc = $png.CreationTimeUtc
                $out.LastWriteTimeUtc = $png.LastWriteTimeUtc
                if (-not $KeepOriginals) {
                    Remove-Item -LiteralPath $png.FullName -Force
                }
                Update-Stats -Field 'Converted' -BytesIn $png.Length -BytesOut $out.Length
                Write-LogLine "OK-EXISTING`t$($png.Name)`t$($png.Length)`t$($out.Length)"
                continue
            }

            $null = & $Cjxl $png.FullName $partial -d $Distance -e $Effort --num_threads $Threads --quiet 2>&1
            if ($LASTEXITCODE -ne 0 -or -not (Test-ValidJxlInner -JxlPath $partial -Tool $Jxlinfo)) {
                if (Test-Path -LiteralPath $partial) {
                    Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
                }
                Update-Stats -Field 'Failed'
                Write-LogLine "FAIL`t$($png.Name)`tencode/verify failed"
                continue
            }

            if (Test-Path -LiteralPath $dest) {
                Remove-Item -LiteralPath $dest -Force
            }
            Move-Item -LiteralPath $partial -Destination $dest -Force

            $out = Get-Item -LiteralPath $dest
            $out.CreationTimeUtc = $png.CreationTimeUtc
            $out.LastWriteTimeUtc = $png.LastWriteTimeUtc

            if (-not $KeepOriginals) {
                Remove-Item -LiteralPath $png.FullName -Force
            }

            Update-Stats -Field 'Converted' -BytesIn $png.Length -BytesOut $out.Length
            Write-LogLine "OK`t$($png.Name)`t$($png.Length)`t$($out.Length)"
        } catch {
            Update-Stats -Field 'Failed'
            Write-LogLine "FAIL`t$pngPath`t$($_.Exception.Message)"
            if ($partial -and (Test-Path -LiteralPath $partial)) {
                Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
            }
        }
    }
}

$pool = [runspacefactory]::CreateRunspacePool(1, $Workers)
$pool.ApartmentState = 'MTA'
$pool.Open()

$runspaces = @()
for ($i = 0; $i -lt $Workers; $i++) {
    $ps = [powershell]::Create()
    $ps.RunspacePool = $pool
    [void]$ps.AddScript($workerScript)
    [void]$ps.AddArgument($queue)
    [void]$ps.AddArgument($sync)
    [void]$ps.AddArgument($cjxl)
    [void]$ps.AddArgument($jxlinfo)
    [void]$ps.AddArgument($Effort)
    [void]$ps.AddArgument($effectiveDistance)
    [void]$ps.AddArgument($ThreadsPerWorker)
    [void]$ps.AddArgument([bool]$KeepOriginals)
    [void]$ps.AddArgument($SkipNewerThanSeconds)
    [void]$ps.AddArgument($logPath)
    $runspaces += [pscustomobject]@{
        Pipe   = $ps
        Handle = $ps.BeginInvoke()
    }
}

$total = $work.Count
$started = Get-Date
try {
    do {
        Start-Sleep -Seconds 5
        $done = $sync.Converted + $sync.Failed + $sync.Skipped
        $remaining = $total - $done
        $elapsed = (Get-Date) - $started
        $rate = if ($elapsed.TotalSeconds -gt 0) { $done / $elapsed.TotalSeconds } else { 0 }
        $etaSec = if ($rate -gt 0) { [int]($remaining / $rate) } else { 0 }
        $savedGiB = [math]::Round(($sync.BytesIn - $sync.BytesOut) / 1GB, 2)
        Write-Host ("{0}/{1}  ok={2} fail={3} skip={4}  saved={5} GiB  elapsed={6:hh\:mm\:ss}  eta={7:hh\:mm\:ss}" -f `
                $done, $total, $sync.Converted, $sync.Failed, $sync.Skipped, $savedGiB, $elapsed, ([TimeSpan]::FromSeconds($etaSec)))
        $alive = @($runspaces | Where-Object { -not $_.Handle.IsCompleted }).Count
    } while ($alive -gt 0)
} finally {
    foreach ($rs in $runspaces) {
        try {
            $null = $rs.Pipe.EndInvoke($rs.Handle)
            $errs = $rs.Pipe.Streams.Error
            foreach ($err in $errs) {
                Add-Content -LiteralPath $logPath -Value "WORKER-ERROR`t$err"
            }
        } catch {
            Add-Content -LiteralPath $logPath -Value "WORKER-ERROR`t$($_.Exception.Message)"
        } finally {
            $rs.Pipe.Dispose()
        }
    }
    $pool.Close()
    $pool.Dispose()
}

$footer = "=== done converted=$($sync.Converted) failed=$($sync.Failed) skipped=$($sync.Skipped) savedBytes=$($sync.BytesIn - $sync.BytesOut) ==="
Add-Content -LiteralPath $logPath -Value $footer
Write-Host $footer
if ($sync.Failed -gt 0) { exit 1 }
exit 0
