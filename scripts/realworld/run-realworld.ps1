<#
.SYNOPSIS
    Real-world end-to-end verification harness for Skipper across public repositories and failure fixtures.

.DESCRIPTION
    Clones curated public repositories and generates local synthetic failure fixtures outside the Skipper workspace,
    runs Skipper's build/up/status/logs/down lifecycle against local Docker, checks stdout JSON hygiene and stderr
    panic safety, tears down all containers, and emits report.md and report.json.

    Compatible with Windows PowerShell 5.1 and PowerShell 7+ (pwsh) on Linux/macOS.
#>
[CmdletBinding()]
param(
    [string]$Manifest = "",
    [string]$WorkDir = "",
    [string]$ResultsDir = "",
    [string]$Skipper = "",
    [string[]]$Only = @(),
    [int]$StageTimeoutSec = 0,
    [switch]$KeepClones
)

$ErrorActionPreference = "Stop"

function Test-OnWindows {
    if ($env:OS -eq "Windows_NT") {
        return $true
    }
    if ($IsWindows -eq $true) {
        return $true
    }
    return $false
}

function Get-AbsolutePathNormalized {
    param([string]$PathValue)
    $full = [System.IO.Path]::GetFullPath($PathValue)
    return $full.TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
}

function Test-PathIsInside {
    param(
        [string]$CandidatePath,
        [string]$ParentPath
    )
    $cand = Get-AbsolutePathNormalized -PathValue $CandidatePath
    $parent = Get-AbsolutePathNormalized -PathValue $ParentPath
    $sep = [System.IO.Path]::DirectorySeparatorChar
    if ($onWindows) {
        $candCmp = $cand.ToLowerInvariant()
        $parentCmp = $parent.ToLowerInvariant()
    } else {
        $candCmp = $cand
        $parentCmp = $parent
    }
    if ($candCmp -eq $parentCmp) {
        return $true
    }
    return $candCmp.StartsWith($parentCmp + $sep)
}

function Get-StageTimeout {
    param(
        [string]$StageName,
        [int]$OverrideSec
    )
    if ($OverrideSec -gt 0) {
        return $OverrideSec
    }
    if ($StageName -eq "build" -or $StageName -eq "up") {
        return 1800
    }
    return 300
}

function Read-TextSafe {
    param([string]$FilePath)
    if (-not (Test-Path $FilePath)) {
        return ""
    }
    try {
        return [System.IO.File]::ReadAllText($FilePath)
    } catch {
        return ""
    }
}

function Get-FirstErrorLine {
    param([string]$Text)
    if ([string]::IsNullOrWhiteSpace($Text)) {
        return ""
    }
    $lines = $Text -split "\r?\n"
    foreach ($ln in $lines) {
        $trimmed = $ln.Trim()
        if ($trimmed -ne "") {
            if ($trimmed.Length -gt 140) {
                return $trimmed.Substring(0, 137) + "..."
            }
            return $trimmed
        }
    }
    return ""
}

function Get-BestErrorLine {
    param([string]$Text)
    if ([string]::IsNullOrWhiteSpace($Text)) {
        return ""
    }
    $lines = $Text -split "\r?\n"
    $selected = ""
    for ($i = $lines.Count - 1; $i -ge 0; $i--) {
        $trimmed = $lines[$i].Trim()
        if ($trimmed.StartsWith("Error:")) {
            $selected = $trimmed
            break
        }
    }
    if ($selected -eq "") {
        for ($i = $lines.Count - 1; $i -ge 0; $i--) {
            $trimmed = $lines[$i].Trim()
            if ($trimmed -ne "" -and ($trimmed.Contains("ERROR") -or ($trimmed -match "failed"))) {
                $selected = $trimmed
                break
            }
        }
    }
    if ($selected -eq "") {
        return Get-FirstErrorLine -Text $Text
    }
    if ($selected.Length -gt 140) {
        return $selected.Substring(0, 137) + "..."
    }
    return $selected
}

function Escape-MarkdownCell {
    param([string]$Text)
    if ([string]::IsNullOrEmpty($Text)) {
        return "-"
    }
    $clean = ($Text -replace "\r?\n", " ") -replace "\|", "/"
    $clean = $clean.Trim()
    if ($clean -eq "") {
        return "-"
    }
    return $clean
}

function Invoke-SkipperStage {
    param(
        [string]$SkipperExe,
        [string]$StageName,
        [string[]]$CliArgs,
        [string]$LogDir,
        [int]$TimeoutSec,
        [bool]$ValidateJson
    )

    if (-not (Test-Path $LogDir)) {
        New-Item -ItemType Directory -Path $LogDir -Force | Out-Null
    }

    $stdoutPath = Join-Path $LogDir ($StageName + ".stdout")
    $stderrPath = Join-Path $LogDir ($StageName + ".stderr")
    if (Test-Path $stdoutPath) { Remove-Item -Force $stdoutPath -ErrorAction SilentlyContinue }
    if (Test-Path $stderrPath) { Remove-Item -Force $stderrPath -ErrorAction SilentlyContinue }

    $procArgs = @()
    foreach ($a in $CliArgs) {
        if ($onWindows -and ($a -match '[\s"]')) {
            $escaped = $a -replace '"', '\"'
            $procArgs += ('"' + $escaped + '"')
        } else {
            $procArgs += $a
        }
    }

    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $startParams = @{
        FilePath               = $SkipperExe
        NoNewWindow            = $true
        PassThru               = $true
        RedirectStandardOutput = $stdoutPath
        RedirectStandardError  = $stderrPath
        WorkingDirectory       = (Get-Location).Path
    }
    if ($procArgs.Count -gt 0) {
        $startParams["ArgumentList"] = $procArgs
    }

    $proc = Start-Process @startParams
    # Cache process handle in PS 5.1 so ExitCode is populated after exit
    try {
        $null = $proc.Handle
    } catch {
    }
    $timeoutMs = $TimeoutSec * 1000
    $finished = $proc.WaitForExit($timeoutMs)
    if (-not $finished) {
        try {
            $proc.Kill()
            $null = $proc.WaitForExit(5000)
        } catch {
            Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        }
        $sw.Stop()
        $stderrText = Read-TextSafe -FilePath $stderrPath
        return [PSCustomObject]@{
            stage       = $StageName
            status      = "TIMEOUT"
            exitCode    = -1
            durationSec = [Math]::Round($sw.Elapsed.TotalSeconds, 2)
            validJson   = $false
            panic       = $false
            error       = ("stage timed out after " + $TimeoutSec + "s")
            stdoutPath  = $stdoutPath
            stderrPath  = $stderrPath
            stdout      = (Read-TextSafe -FilePath $stdoutPath)
            stderr      = $stderrText
            parsedJson  = $null
        }
    }

    # Ensure async stream flush completes on .NET Process wrapper
    try {
        $proc.WaitForExit()
    } catch {
    }
    $sw.Stop()

    $exitCode = $proc.ExitCode
    $stdoutText = Read-TextSafe -FilePath $stdoutPath
    $stderrText = Read-TextSafe -FilePath $stderrPath
    $hasPanic = ($stderrText -match "panic:") -or ($stderrText -match "goroutine\s+\d*")

    $jsonValid = $true
    $parsedObj = $null
    $stageStatus = "PASS"
    $errMsg = ""

    if ($hasPanic) {
        $stageStatus = "PANIC"
        $errMsg = Get-FirstErrorLine -Text $stderrText
        if ($errMsg -eq "") { $errMsg = "Go panic detected on stderr" }
        $jsonValid = $false
    } elseif ($exitCode -ne 0) {
        $stageStatus = "FAIL"
        $errMsg = Get-BestErrorLine -Text $stderrText
        if ($errMsg -eq "") {
            $errMsg = Get-BestErrorLine -Text $stdoutText
        }
        if ($errMsg -eq "") {
            $errMsg = ("exit code " + $exitCode)
        }
        $jsonValid = $false
    } elseif ($ValidateJson) {
        if ([string]::IsNullOrWhiteSpace($stdoutText)) {
            $jsonValid = $false
            $stageStatus = "FAIL"
            $errMsg = "invalid JSON on stdout (empty output)"
        } else {
            try {
                $parsedObj = $stdoutText | ConvertFrom-Json
            } catch {
                $jsonValid = $false
                $stageStatus = "FAIL"
                $errMsg = "invalid JSON on stdout"
            }
        }
    }

    return [PSCustomObject]@{
        stage       = $StageName
        status      = $stageStatus
        exitCode    = $exitCode
        durationSec = [Math]::Round($sw.Elapsed.TotalSeconds, 2)
        validJson   = $jsonValid
        panic       = $hasPanic
        error       = $errMsg
        stdoutPath  = $stdoutPath
        stderrPath  = $stderrPath
        stdout      = $stdoutText
        stderr      = $stderrText
        parsedJson  = $parsedObj
    }
}

function Invoke-ComposeStatusPoll {
    param(
        [string]$SkipperExe,
        [string]$LogDir,
        [int]$TimeoutSec
    )
    $deadline = (Get-Date).AddSeconds(120)
    $swTotal = [System.Diagnostics.Stopwatch]::StartNew()
    $attemptTimeout = [Math]::Min($TimeoutSec, 60)
    $lastResult = $null

    while ($true) {
        $lastResult = Invoke-SkipperStage -SkipperExe $SkipperExe -StageName "status" -CliArgs @("status", "--format", "json") -LogDir $LogDir -TimeoutSec $attemptTimeout -ValidateJson $true
        if ($lastResult.status -eq "PANIC" -or $lastResult.status -eq "TIMEOUT" -or $lastResult.status -eq "FAIL") {
            $swTotal.Stop()
            $lastResult.durationSec = [Math]::Round($swTotal.Elapsed.TotalSeconds, 2)
            return $lastResult
        }

        $services = @()
        if ($null -ne $lastResult.parsedJson -and $null -ne $lastResult.parsedJson.services) {
            $services = @($lastResult.parsedJson.services)
        }

        $allRunning = ($services.Count -gt 0)
        $nonRunning = @()
        $exitedEarly = $false
        foreach ($svc in $services) {
            $st = ""
            if ($null -ne $svc.state) {
                $st = ([string]$svc.state).Trim().ToLowerInvariant()
            }
            if ($st -ne "running") {
                $allRunning = $false
                $svcLabel = if ($svc.service) { $svc.service } else { $svc.name }
                $nonRunning += ($svcLabel + "=" + $st)
                if ($st -eq "exited" -or $st -eq "dead") {
                    $exitedEarly = $true
                }
            }
        }

        if ($allRunning) {
            $swTotal.Stop()
            $lastResult.durationSec = [Math]::Round($swTotal.Elapsed.TotalSeconds, 2)
            return $lastResult
        }

        if ($exitedEarly -or (Get-Date) -ge $deadline) {
            $swTotal.Stop()
            $lastResult.status = "FAIL"
            $lastResult.durationSec = [Math]::Round($swTotal.Elapsed.TotalSeconds, 2)
            if ($services.Count -eq 0) {
                $lastResult.error = "compose status returned 0 services"
            } elseif ($exitedEarly) {
                $lastResult.error = ("service exited prematurely: " + ($nonRunning -join ", "))
            } else {
                $lastResult.error = ("services not running within 120s: " + ($nonRunning -join ", "))
            }
            return $lastResult
        }

        Start-Sleep -Seconds 5
    }
}

function Invoke-SingleStatusPoll {
    param(
        [string]$SkipperExe,
        [string]$LogDir,
        [int]$TimeoutSec
    )
    $deadline = (Get-Date).AddSeconds(120)
    $swTotal = [System.Diagnostics.Stopwatch]::StartNew()
    $attemptTimeout = [Math]::Min($TimeoutSec, 60)
    $lastResult = $null

    while ($true) {
        $lastResult = Invoke-SkipperStage -SkipperExe $SkipperExe -StageName "status" -CliArgs @("status", "--format", "json") -LogDir $LogDir -TimeoutSec $attemptTimeout -ValidateJson $true
        if ($lastResult.status -eq "PANIC" -or $lastResult.status -eq "TIMEOUT" -or $lastResult.status -eq "FAIL") {
            $swTotal.Stop()
            $lastResult.durationSec = [Math]::Round($swTotal.Elapsed.TotalSeconds, 2)
            return $lastResult
        }

        $stateVal = ""
        if ($null -ne $lastResult.parsedJson -and $null -ne $lastResult.parsedJson.state) {
            $stateVal = ([string]$lastResult.parsedJson.state).Trim().ToLowerInvariant()
        }

        if ($stateVal -eq "running") {
            $swTotal.Stop()
            $lastResult.durationSec = [Math]::Round($swTotal.Elapsed.TotalSeconds, 2)
            return $lastResult
        }

        if ($stateVal -eq "stopped" -or $stateVal -eq "exited" -or $stateVal -eq "error" -or (Get-Date) -ge $deadline) {
            $swTotal.Stop()
            $lastResult.status = "FAIL"
            $lastResult.durationSec = [Math]::Round($swTotal.Elapsed.TotalSeconds, 2)
            if ($stateVal -eq "stopped" -or $stateVal -eq "exited" -or $stateVal -eq "error") {
                $lastResult.error = ("workload state became '" + $stateVal + "'")
            } else {
                $lastResult.error = ("workload state '" + $stateVal + "' did not reach running within 120s")
            }
            return $lastResult
        }

        Start-Sleep -Seconds 5
    }
}

function Invoke-HttpProbeStage {
    param(
        [int]$Port,
        [string]$LogDir
    )
    $stdoutPath = Join-Path $LogDir "http.stdout"
    $stderrPath = Join-Path $LogDir "http.stderr"
    $uri = "http://localhost:" + $Port
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $maxAttempts = 4
    $lastErr = ""
    $statusCode = 0

    for ($i = 1; $i -le $maxAttempts; $i++) {
        try {
            $resp = Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 10
            $statusCode = [int]$resp.StatusCode
            if ($statusCode -gt 0 -and $statusCode -lt 500) {
                $sw.Stop()
                [System.IO.File]::WriteAllText($stdoutPath, ("HTTP " + $statusCode + " from " + $uri + "`n"))
                [System.IO.File]::WriteAllText($stderrPath, "")
                return [PSCustomObject]@{
                    stage       = "http"
                    status      = "PASS"
                    exitCode    = 0
                    durationSec = [Math]::Round($sw.Elapsed.TotalSeconds, 2)
                    validJson   = $true
                    panic       = $false
                    error       = ""
                    stdoutPath  = $stdoutPath
                    stderrPath  = $stderrPath
                }
            }
            $lastErr = ("HTTP " + $statusCode + " from " + $uri)
        } catch {
            $exResp = $_.Exception.Response
            if ($null -ne $exResp -and $null -ne $exResp.StatusCode) {
                try {
                    $statusCode = [int]$exResp.StatusCode
                } catch {
                    $statusCode = 0
                }
                if ($statusCode -gt 0 -and $statusCode -lt 500) {
                    $sw.Stop()
                    [System.IO.File]::WriteAllText($stdoutPath, ("HTTP " + $statusCode + " from " + $uri + "`n"))
                    [System.IO.File]::WriteAllText($stderrPath, "")
                    return [PSCustomObject]@{
                        stage       = "http"
                        status      = "PASS"
                        exitCode    = 0
                        durationSec = [Math]::Round($sw.Elapsed.TotalSeconds, 2)
                        validJson   = $true
                        panic       = $false
                        error       = ""
                        stdoutPath  = $stdoutPath
                        stderrPath  = $stderrPath
                    }
                }
                $lastErr = ("HTTP " + $statusCode + " from " + $uri)
            } else {
                $lastErr = $_.Exception.Message
            }
        }
        if ($i -lt $maxAttempts) {
            Start-Sleep -Seconds 2
        }
    }

    $sw.Stop()
    [System.IO.File]::WriteAllText($stdoutPath, "")
    [System.IO.File]::WriteAllText($stderrPath, ($lastErr + "`n"))
    return [PSCustomObject]@{
        stage       = "http"
        status      = "FAIL"
        exitCode    = 1
        durationSec = [Math]::Round($sw.Elapsed.TotalSeconds, 2)
        validJson   = $false
        panic       = $false
        error       = Get-FirstErrorLine -Text $lastErr
        stdoutPath  = $stdoutPath
        stderrPath  = $stderrPath
    }
}

function Initialize-FixtureWorkspace {
    param(
        [string]$FixtureType,
        [string]$TargetDir
    )
    switch ($FixtureType) {
        "broken-compose" {
            $badYaml = "services:`n  web:`n    image: [invalid`n"
            [System.IO.File]::WriteAllText((Join-Path $TargetDir "docker-compose.yml"), $badYaml)
        }
        "empty-dir" {
            # Intentionally empty directory
        }
        "bad-dockerfile" {
            $badDf = "FROM nonexistent-image-xyz:0`nCMD [`"echo`", `"unreachable`"]`n"
            [System.IO.File]::WriteAllText((Join-Path $TargetDir "Dockerfile"), $badDf)
        }
        default {
            throw ("Unknown fixture type: " + $FixtureType)
        }
    }
}

function Get-DockerContainerIds {
    $prevPref = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $raw = & docker ps -aq 2>$null
    $dcExit = $LASTEXITCODE
    $ErrorActionPreference = $prevPref
    if ($dcExit -ne 0 -or $null -eq $raw) {
        return @()
    }
    $ids = @()
    foreach ($line in $raw) {
        $t = ([string]$line).Trim()
        if ($t -ne "") {
            $ids += $t
        }
    }
    return $ids
}

function Get-DockerRunningContainerIds {
    $prevPref = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $raw = & docker ps -q 2>$null
    $dcExit = $LASTEXITCODE
    $ErrorActionPreference = $prevPref
    if ($dcExit -ne 0 -or $null -eq $raw) {
        return @()
    }
    $ids = @()
    foreach ($line in $raw) {
        $t = ([string]$line).Trim()
        if ($t -ne "") {
            $ids += $t
        }
    }
    return $ids
}

$onWindows = Test-OnWindows
$repoRoot = Get-AbsolutePathNormalized -PathValue (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))

if ([string]::IsNullOrWhiteSpace($Manifest)) {
    $Manifest = Join-Path $PSScriptRoot "repos.json"
}
if ([string]::IsNullOrWhiteSpace($WorkDir)) {
    $WorkDir = Join-Path ([System.IO.Path]::GetTempPath()) "skipper-realworld"
}
if ([string]::IsNullOrWhiteSpace($ResultsDir)) {
    $ResultsDir = Join-Path $repoRoot "realworld-results"
}
if ([string]::IsNullOrWhiteSpace($Skipper)) {
    if ($onWindows) {
        $Skipper = Join-Path (Join-Path $repoRoot "bin") "skipper.exe"
    } else {
        $Skipper = Join-Path (Join-Path $repoRoot "bin") "skipper"
    }
}

# Preflight checks
if (-not (Test-Path $Manifest)) {
    Write-Error ("Manifest file not found: " + $Manifest)
    exit 2
}

$workDirAbs = Get-AbsolutePathNormalized -PathValue $WorkDir
if (Test-PathIsInside -CandidatePath $workDirAbs -ParentPath $repoRoot) {
    Write-Error ("WorkDir must be OUTSIDE the repository root. Got: " + $workDirAbs)
    exit 2
}

if (-not (Test-Path $Skipper)) {
    Write-Error ("Skipper binary not found at: " + $Skipper)
    exit 2
}
$skipperAbs = (Resolve-Path $Skipper).Path

$verOut = & $skipperAbs --version 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Error ("Skipper binary check (--version) failed: " + ($verOut -join " "))
    exit 2
}
Write-Host ("Preflight: " + ($verOut -join " "))

$prevPref = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$null = & docker info 2>&1
$dockerPreflightExit = $LASTEXITCODE
$ErrorActionPreference = $prevPref
if ($dockerPreflightExit -ne 0) {
    Write-Error "Preflight failed: 'docker info' returned non-zero. Start Docker Desktop / Docker daemon and retry."
    exit 2
}
Write-Host "Preflight: Docker daemon is reachable."

if (-not (Test-Path $workDirAbs)) {
    New-Item -ItemType Directory -Path $workDirAbs -Force | Out-Null
}

$timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss")
$resultsRootAbs = Get-AbsolutePathNormalized -PathValue $ResultsDir
$runResultsDir = Join-Path $resultsRootAbs $timestamp
New-Item -ItemType Directory -Path $runResultsDir -Force | Out-Null

# Run skipper doctor --format json once and save output
$doctorResult = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "doctor" -CliArgs @("doctor", "--format", "json") -LogDir $runResultsDir -TimeoutSec 60 -ValidateJson $true
Write-Host ("Preflight: skipper doctor completed (exit=" + $doctorResult.exitCode + ", validJson=" + $doctorResult.validJson + ")")

$manifestRaw = [System.IO.File]::ReadAllText($Manifest)
$parsedManifest = ConvertFrom-Json -InputObject $manifestRaw
if ($parsedManifest -is [System.Array]) {
    $entries = $parsedManifest
} elseif ($null -ne $parsedManifest) {
    $entries = @($parsedManifest)
} else {
    $entries = @()
}

if ($Only.Count -gt 0) {
    $onlySet = @{}
    foreach ($o in $Only) {
        foreach ($part in ($o -split ",")) {
            $trimmed = $part.Trim()
            if ($trimmed -ne "") {
                $onlySet[$trimmed] = $true
            }
        }
    }
    $filtered = @()
    foreach ($e in $entries) {
        if ($onlySet.ContainsKey($e.name)) {
            $filtered += $e
        }
    }
    $entries = $filtered
    if ($entries.Count -eq 0) {
        Write-Error ("No manifest entries matched -Only filter: " + ($Only -join ", "))
        exit 2
    }
}

$origLocation = (Get-Location).Path
$results = @()
$dockerDown = $false
$dockerSkippedCount = 0

for ($idx = 0; $idx -lt $entries.Count; $idx++) {
    $entry = $entries[$idx]
    $name = [string]$entry.name
    $kind = [string]$entry.kind
    $expect = [string]$entry.expect
    if ([string]::IsNullOrWhiteSpace($expect)) { $expect = "pass" }

    # Mid-run Docker health check before cloning/building next entry
    $prevPref = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $null = & docker info 2>&1
    $dockerMidExit = $LASTEXITCODE
    $ErrorActionPreference = $prevPref
    if ($dockerMidExit -ne 0) {
        $dockerDown = $true
        Write-Warning "Docker daemon became unavailable mid-run; skipping remaining entries."
        for ($rem = $idx; $rem -lt $entries.Count; $rem++) {
            $remEntry = $entries[$rem]
            $remExpect = [string]$remEntry.expect
            if ([string]::IsNullOrWhiteSpace($remExpect)) { $remExpect = "pass" }
            $dockerSkippedCount++
            $results += [PSCustomObject]@{
                name               = [string]$remEntry.name
                kind               = [string]$remEntry.kind
                expect             = $remExpect
                verdict            = "SKIPPED"
                passed             = $true
                reason             = "docker daemon unavailable"
                leftoverContainers = @()
                stoppedLeftovers   = @()
                stages             = @{}
            }
        }
        break
    }

    Write-Host ""
    Write-Host ("=== [" + $name + "] kind=" + $kind + " expect=" + $expect + " ===")

    $entryLogDir = Join-Path $runResultsDir $name
    New-Item -ItemType Directory -Path $entryLogDir -Force | Out-Null

    $cloneRoot = Join-Path $workDirAbs $name
    if (Test-Path $cloneRoot) {
        Remove-Item -Recurse -Force $cloneRoot -ErrorAction SilentlyContinue
    }
    New-Item -ItemType Directory -Path $cloneRoot -Force | Out-Null

    $projectDir = $cloneRoot
    $cloneOk = $true
    $cloneErr = ""

    if ($null -ne $entry.fixture -and -not [string]::IsNullOrWhiteSpace([string]$entry.fixture)) {
        try {
            Initialize-FixtureWorkspace -FixtureType ([string]$entry.fixture) -TargetDir $cloneRoot
        } catch {
            $cloneOk = $false
            $cloneErr = $_.Exception.Message
        }
    } else {
        $url = [string]$entry.url
        $hasSubdir = ($null -ne $entry.subdir -and -not [string]::IsNullOrWhiteSpace([string]$entry.subdir))
        $hasCommit = ($null -ne $entry.commit -and -not [string]::IsNullOrWhiteSpace([string]$entry.commit))

        if ($hasCommit) {
            $commitSha = ([string]$entry.commit).Trim()
            $prevPref = $ErrorActionPreference
            $ErrorActionPreference = "Continue"
            $initOut = & git -C $cloneRoot init -q 2>&1
            $initExit = $LASTEXITCODE
            $ErrorActionPreference = $prevPref
            if ($initExit -ne 0) {
                $cloneOk = $false
                $cloneErr = ("git init failed: " + (Get-FirstErrorLine -Text ($initOut -join "`n")))
            } else {
                if ($hasSubdir) {
                    $sub = [string]$entry.subdir
                    $prevPref = $ErrorActionPreference
                    $ErrorActionPreference = "Continue"
                    $scInitOut = & git -C $cloneRoot sparse-checkout init --cone 2>&1
                    $scInitExit = $LASTEXITCODE
                    if ($scInitExit -eq 0) {
                        $scSetOut = & git -C $cloneRoot sparse-checkout set $sub 2>&1
                        $scSetExit = $LASTEXITCODE
                    } else {
                        $scSetOut = @()
                        $scSetExit = $scInitExit
                    }
                    $ErrorActionPreference = $prevPref
                    if ($scInitExit -ne 0) {
                        $cloneOk = $false
                        $cloneErr = ("git sparse-checkout init failed: " + (Get-FirstErrorLine -Text ($scInitOut -join "`n")))
                    } elseif ($scSetExit -ne 0) {
                        $cloneOk = $false
                        $cloneErr = ("git sparse-checkout set failed: " + (Get-FirstErrorLine -Text ($scSetOut -join "`n")))
                    }
                }

                if ($cloneOk) {
                    $fetchArgs = @("-C", $cloneRoot, "fetch", "--depth", "1")
                    if ($hasSubdir) {
                        $fetchArgs += "--filter=blob:none"
                    }
                    $fetchArgs += @($url, $commitSha)

                    $prevPref = $ErrorActionPreference
                    $ErrorActionPreference = "Continue"
                    $fetchOut = & git @fetchArgs 2>&1
                    $fetchExit = $LASTEXITCODE
                    if ($fetchExit -eq 0) {
                        $coOut = & git -C $cloneRoot checkout -q --detach FETCH_HEAD 2>&1
                        $coExit = $LASTEXITCODE
                    } else {
                        $coOut = @()
                        $coExit = $fetchExit
                    }
                    $ErrorActionPreference = $prevPref

                    if ($fetchExit -ne 0) {
                        $cloneOk = $false
                        $cloneErr = ("git fetch commit failed: " + (Get-FirstErrorLine -Text ($fetchOut -join "`n")))
                    } elseif ($coExit -ne 0) {
                        $cloneOk = $false
                        $cloneErr = ("git checkout FETCH_HEAD failed: " + (Get-FirstErrorLine -Text ($coOut -join "`n")))
                    }
                }
            }
        } else {
            $cloneArgs = @("clone", "--depth", "1")
            if ($null -ne $entry.ref -and -not [string]::IsNullOrWhiteSpace([string]$entry.ref)) {
                $cloneArgs += @("--branch", [string]$entry.ref)
            }
            if ($hasSubdir) {
                $cloneArgs += @("--filter=blob:none", "--sparse")
            }
            $cloneArgs += @($url, $cloneRoot)

            $prevPref = $ErrorActionPreference
            $ErrorActionPreference = "Continue"
            $cloneOutput = & git @cloneArgs 2>&1
            $gitExit = $LASTEXITCODE
            $ErrorActionPreference = $prevPref

            if ($gitExit -ne 0) {
                $cloneOk = $false
                $cloneErr = ("git clone failed: " + (Get-FirstErrorLine -Text ($cloneOutput -join "`n")))
            } elseif ($hasSubdir) {
                $sub = [string]$entry.subdir
                $prevPref = $ErrorActionPreference
                $ErrorActionPreference = "Continue"
                $sparseOut = & git -C $cloneRoot sparse-checkout set $sub 2>&1
                $sparseExit = $LASTEXITCODE
                $ErrorActionPreference = $prevPref
                if ($sparseExit -ne 0) {
                    $cloneOk = $false
                    $cloneErr = ("git sparse-checkout failed: " + (Get-FirstErrorLine -Text ($sparseOut -join "`n")))
                }
            }
        }

        if ($cloneOk -and $hasSubdir) {
            $sub = [string]$entry.subdir
            $relParts = $sub -split "[\\/]"
            $resolvedSub = $cloneRoot
            foreach ($part in $relParts) {
                if ($part -ne "") {
                    $resolvedSub = Join-Path $resolvedSub $part
                }
            }
            if (-not (Test-Path $resolvedSub)) {
                $cloneOk = $false
                $cloneErr = ("subdir not found after sparse checkout: " + $sub)
            } else {
                $projectDir = $resolvedSub
            }
        }
    }

    if (-not $cloneOk) {
        Write-Warning ("SKIPPED " + $name + ": " + $cloneErr)
        $results += [PSCustomObject]@{
            name                = $name
            kind                = $kind
            expect              = $expect
            verdict             = "SKIPPED"
            passed              = $true
            reason              = $cloneErr
            leftoverContainers  = @()
            stoppedLeftovers    = @()
            stages              = @{}
        }
        if (-not $KeepClones -and (Test-Path $cloneRoot)) {
            Remove-Item -Recurse -Force $cloneRoot -ErrorAction SilentlyContinue
        }
        continue
    }

    $beforeAllContainerIds = Get-DockerContainerIds
    $beforeRunningContainerIds = Get-DockerRunningContainerIds
    $stageMap = @{}
    $downAttempted = $false
    $anyPanic = $false
    $anyTimeout = $false

    try {
        Set-Location $projectDir

        if ($kind -eq "compose") {
            # 1. build
            $bRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "build" -CliArgs @("build", "--format", "json") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "build" $StageTimeoutSec) -ValidateJson $true
            $stageMap["build"] = $bRes
            if ($bRes.panic) { $anyPanic = $true }
            if ($bRes.status -eq "TIMEOUT") { $anyTimeout = $true }

            if ($bRes.status -eq "PASS") {
                # 2. up --build
                $uRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "up" -CliArgs @("up", "--build", "--format", "json") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "up" $StageTimeoutSec) -ValidateJson $true
                $stageMap["up"] = $uRes
                if ($uRes.panic) { $anyPanic = $true }
                if ($uRes.status -eq "TIMEOUT") { $anyTimeout = $true }

                if ($uRes.status -eq "PASS") {
                    # 3. poll status
                    $sRes = Invoke-ComposeStatusPoll -SkipperExe $skipperAbs -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "status" $StageTimeoutSec)
                    $stageMap["status"] = $sRes
                    if ($sRes.panic) { $anyPanic = $true }
                    if ($sRes.status -eq "TIMEOUT") { $anyTimeout = $true }

                    # 4. optional HTTP probe
                    if ($sRes.status -eq "PASS" -and $null -ne $entry.httpPort -and [int]$entry.httpPort -gt 0) {
                        $hRes = Invoke-HttpProbeStage -Port ([int]$entry.httpPort) -LogDir $entryLogDir
                        $stageMap["http"] = $hRes
                    }
                }

                # 5. down --volumes
                $dRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "down" -CliArgs @("down", "--volumes", "--format", "json") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "down" $StageTimeoutSec) -ValidateJson $true
                $stageMap["down"] = $dRes
                $downAttempted = $true
                if ($dRes.panic) { $anyPanic = $true }
                if ($dRes.status -eq "TIMEOUT") { $anyTimeout = $true }
            }
        } else {
            # Single-service lifecycle
            # 1. init (only if no skipper.yaml)
            if (-not (Test-Path "skipper.yaml") -and -not (Test-Path "app.yaml")) {
                $iRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "init" -CliArgs @("init", "--format", "json") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "init" $StageTimeoutSec) -ValidateJson $true
                $stageMap["init"] = $iRes
                if ($iRes.panic) { $anyPanic = $true }
                if ($iRes.status -eq "TIMEOUT") { $anyTimeout = $true }
            }

            $initOk = (-not $stageMap.ContainsKey("init")) -or ($stageMap["init"].status -eq "PASS")
            if ($initOk) {
                # 2. build
                $bRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "build" -CliArgs @("build", "--format", "json") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "build" $StageTimeoutSec) -ValidateJson $true
                $stageMap["build"] = $bRes
                if ($bRes.panic) { $anyPanic = $true }
                if ($bRes.status -eq "TIMEOUT") { $anyTimeout = $true }

                if ($bRes.status -eq "PASS") {
                    # 3. up
                    $uRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "up" -CliArgs @("up", "--format", "json") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "up" $StageTimeoutSec) -ValidateJson $true
                    $stageMap["up"] = $uRes
                    if ($uRes.panic) { $anyPanic = $true }
                    if ($uRes.status -eq "TIMEOUT") { $anyTimeout = $true }

                    if ($uRes.status -eq "PASS") {
                        # 4. poll status
                        $sRes = Invoke-SingleStatusPoll -SkipperExe $skipperAbs -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "status" $StageTimeoutSec)
                        $stageMap["status"] = $sRes
                        if ($sRes.panic) { $anyPanic = $true }
                        if ($sRes.status -eq "TIMEOUT") { $anyTimeout = $true }

                        # 5. optional HTTP probe
                        if ($sRes.status -eq "PASS" -and $null -ne $entry.httpPort -and [int]$entry.httpPort -gt 0) {
                            $hRes = Invoke-HttpProbeStage -Port ([int]$entry.httpPort) -LogDir $entryLogDir
                            $stageMap["http"] = $hRes
                        }

                        # 6. logs --tail 50
                        if ($sRes.status -eq "PASS") {
                            $lRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "logs" -CliArgs @("logs", "--tail", "50") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "logs" $StageTimeoutSec) -ValidateJson $false
                            $stageMap["logs"] = $lRes
                            if ($lRes.panic) { $anyPanic = $true }
                            if ($lRes.status -eq "TIMEOUT") { $anyTimeout = $true }
                        }
                    }

                    # 7. down
                    $dRes = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "down" -CliArgs @("down", "--format", "json") -LogDir $entryLogDir -TimeoutSec (Get-StageTimeout "down" $StageTimeoutSec) -ValidateJson $true
                    $stageMap["down"] = $dRes
                    $downAttempted = $true
                    if ($dRes.panic) { $anyPanic = $true }
                    if ($dRes.status -eq "TIMEOUT") { $anyTimeout = $true }
                }
            }
        }
    } finally {
        if (-not $downAttempted -and (Test-Path $projectDir)) {
            try {
                Set-Location $projectDir
                if ($kind -eq "compose") {
                    $hasCompose = (Test-Path "docker-compose.yml") -or (Test-Path "docker-compose.yaml") -or (Test-Path "compose.yml") -or (Test-Path "compose.yaml")
                    if ($hasCompose) {
                        $null = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "cleanup-down" -CliArgs @("down", "--volumes", "--format", "json") -LogDir $entryLogDir -TimeoutSec 120 -ValidateJson $false
                    }
                } else {
                    $hasSpec = (Test-Path "skipper.yaml") -or (Test-Path "app.yaml")
                    if ($hasSpec) {
                        $null = Invoke-SkipperStage -SkipperExe $skipperAbs -StageName "cleanup-down" -CliArgs @("down", "--format", "json") -LogDir $entryLogDir -TimeoutSec 120 -ValidateJson $false
                    }
                }
            } catch {
            }
        }
        Set-Location $origLocation
        if (-not $KeepClones -and (Test-Path $cloneRoot)) {
            Remove-Item -Recurse -Force $cloneRoot -ErrorAction SilentlyContinue
        }
    }

    $afterRunningIds = Get-DockerRunningContainerIds
    $leftover = @()
    foreach ($cid in $afterRunningIds) {
        if ($beforeRunningContainerIds -notcontains $cid) {
            $leftover += $cid
        }
    }

    $afterAllIds = Get-DockerContainerIds
    $stoppedLeftovers = @()
    foreach ($cid in $afterAllIds) {
        if (($beforeAllContainerIds -notcontains $cid) -and ($leftover -notcontains $cid)) {
            $stoppedLeftovers += $cid
        }
    }

    if ($stoppedLeftovers.Count -gt 0) {
        $prevPref = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        $null = & docker rm -f @stoppedLeftovers 2>$null
        $ErrorActionPreference = $prevPref
    }

    # Evaluate verdict for entry
    $stageOrder = @("init", "build", "up", "status", "http", "logs", "down")
    $firstFailedStage = $null
    $allStagesPass = $true
    foreach ($sn in $stageOrder) {
        if ($stageMap.ContainsKey($sn)) {
            $stObj = $stageMap[$sn]
            if ($stObj.status -ne "PASS") {
                $allStagesPass = $false
                if ($null -eq $firstFailedStage) {
                    $firstFailedStage = $stObj
                }
            }
        }
    }

    $verdict = "PASS"
    $passedExpectation = $false
    $firstErrorLine = ""

    if ($anyPanic) {
        $verdict = "PANIC"
        $passedExpectation = $false
        foreach ($sn in $stageOrder) {
            if ($stageMap.ContainsKey($sn) -and $stageMap[$sn].panic) {
                $firstErrorLine = $stageMap[$sn].error
                break
            }
        }
    } elseif ($anyTimeout) {
        $verdict = "TIMEOUT"
        $passedExpectation = $false
        foreach ($sn in $stageOrder) {
            if ($stageMap.ContainsKey($sn) -and $stageMap[$sn].status -eq "TIMEOUT") {
                $firstErrorLine = $stageMap[$sn].error
                break
            }
        }
    } elseif ($expect -eq "pass") {
        if ($allStagesPass -and $leftover.Count -eq 0) {
            $verdict = "PASS"
            $passedExpectation = $true
        } else {
            $verdict = "FAIL"
            $passedExpectation = $false
            if ($null -ne $firstFailedStage) {
                $firstErrorLine = ($firstFailedStage.stage + ": " + $firstFailedStage.error)
            } elseif ($leftover.Count -gt 0) {
                $firstErrorLine = ("leftover containers after teardown: " + ($leftover -join ", "))
            }
        }
    } else {
        # expect == fail: some stage must exit non-zero cleanly (panic-free, non-empty stderr) and leave no leftover containers
        $cleanFailureFound = $false
        foreach ($sn in $stageOrder) {
            if ($stageMap.ContainsKey($sn)) {
                $stObj = $stageMap[$sn]
                if ($stObj.exitCode -ne 0 -and (-not $stObj.panic) -and (-not [string]::IsNullOrWhiteSpace($stObj.stderr))) {
                    $cleanFailureFound = $true
                    $firstErrorLine = ($stObj.stage + ": " + (Get-BestErrorLine -Text $stObj.stderr))
                    break
                }
            }
        }
        if ($cleanFailureFound -and $leftover.Count -eq 0) {
            $verdict = "PASS"
            $passedExpectation = $true
        } else {
            $verdict = "FAIL"
            $passedExpectation = $false
            if (-not $cleanFailureFound) {
                if ($allStagesPass) {
                    $firstErrorLine = "expected failure, but all stages succeeded"
                } elseif ($null -ne $firstFailedStage) {
                    $firstErrorLine = ($firstFailedStage.stage + ": failed without non-empty stderr message")
                } else {
                    $firstErrorLine = "expected non-zero exit with stderr message"
                }
            } else {
                $firstErrorLine = ("leftover containers after teardown: " + ($leftover -join ", "))
            }
        }
    }

    if ($stoppedLeftovers.Count -gt 0 -and $verdict -eq "PASS") {
        $tdNote = ("teardown note: " + $stoppedLeftovers.Count + " stopped container(s) not removed")
        if ([string]::IsNullOrWhiteSpace($firstErrorLine)) {
            $firstErrorLine = $tdNote
        } else {
            $firstErrorLine = ($firstErrorLine + "; " + $tdNote)
        }
    }

    Write-Host ("Verdict [" + $name + "]: " + $verdict + $(if ($firstErrorLine -ne "") { " (" + $firstErrorLine + ")" } else { "" }))

    # Strip large stdout/stderr blobs before adding to report summary object
    $compactStages = @{}
    foreach ($sn in $stageOrder) {
        if ($stageMap.ContainsKey($sn)) {
            $orig = $stageMap[$sn]
            $compactStages[$sn] = [PSCustomObject]@{
                status      = $orig.status
                exitCode    = $orig.exitCode
                durationSec = $orig.durationSec
                validJson   = $orig.validJson
                panic       = $orig.panic
                error       = $orig.error
            }
        }
    }

    $results += [PSCustomObject]@{
        name               = $name
        kind               = $kind
        expect             = $expect
        verdict            = $verdict
        passed             = $passedExpectation
        reason             = $firstErrorLine
        leftoverContainers = $leftover
        stoppedLeftovers   = $stoppedLeftovers
        stages             = $compactStages
    }
}

# Build summary counts (SKIPPED excluded from evaluated denominator)
$evaluated = @($results | Where-Object { $_.verdict -ne "SKIPPED" })
$passedCount = @($evaluated | Where-Object { $_.passed -eq $true }).Count
$skippedCount = @($results | Where-Object { $_.verdict -eq "SKIPPED" }).Count
$cloneSkippedCount = $skippedCount - $dockerSkippedCount
$totalEval = $evaluated.Count
$anyPanicOverall = (@($results | Where-Object { $_.verdict -eq "PANIC" }).Count -gt 0)

$reportObj = [PSCustomObject]@{
    timestamp     = $timestamp
    skipper       = $skipperAbs
    version       = ($verOut -join " ").Trim()
    doctorExit    = $doctorResult.exitCode
    passedCount   = $passedCount
    totalCount    = $totalEval
    skippedCount  = $skippedCount
    dockerSkipped = $dockerSkippedCount
    dockerDown    = $dockerDown
    results       = $results
}

$reportJsonPath = Join-Path $runResultsDir "report.json"
$reportObj | ConvertTo-Json -Depth 8 | Out-File -FilePath $reportJsonPath -Encoding utf8

# Build Markdown report
$skipNotes = @()
if ($cloneSkippedCount -gt 0) {
    $skipNotes += ($cloneSkippedCount + " skipped due to clone error")
}
if ($dockerSkippedCount -gt 0) {
    $skipNotes += ($dockerSkippedCount + " skipped: Docker daemon became unavailable")
}
$skipSuffix = ""
if ($skipNotes.Count -gt 0) {
    $skipSuffix = (" (" + ($skipNotes -join "; ") + ")")
}

$mdLines = @()
$mdLines += ("# Skipper Real-World Verification Report (" + $timestamp + ")")
$mdLines += ""
$mdLines += ("- **Skipper Binary:** ``" + $skipperAbs + "`` (" + ($verOut -join " ").Trim() + ")")
$mdLines += ("- **Doctor Audit:** exit=" + $doctorResult.exitCode + ", validJson=" + $doctorResult.validJson)
$mdLines += ("- **Summary:** " + $passedCount + "/" + $totalEval + " passed" + $skipSuffix)
$mdLines += ""
$mdLines += "| Repo | Kind | Expect | init | build | up | status | http | logs | down | Verdict | First Error |"
$mdLines += "| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |"

$allStageCols = @("init", "build", "up", "status", "http", "logs", "down")
foreach ($r in $results) {
    $cells = @()
    foreach ($col in $allStageCols) {
        if ($r.stages.ContainsKey($col)) {
            $st = $r.stages[$col]
            $cells += ($st.status + " (" + $st.durationSec + "s)")
        } else {
            $cells += "-"
        }
    }
    $errCell = Escape-MarkdownCell -Text $r.reason
    $row = "| " + $r.name + " | " + $r.kind + " | " + $r.expect + " | " + ($cells -join " | ") + " | **" + $r.verdict + "** | " + $errCell + " |"
    $mdLines += $row
}

$mdLines += ""
$mdLines += ("Summary: " + $passedCount + "/" + $totalEval + " passed" + $skipSuffix)

$reportMdPath = Join-Path $runResultsDir "report.md"
$mdLines | Out-File -FilePath $reportMdPath -Encoding utf8

Write-Host ""
Write-Host ("Report written to:")
Write-Host ("  " + $reportMdPath)
Write-Host ("  " + $reportJsonPath)
Write-Host ("Summary: " + $passedCount + "/" + $totalEval + " passed" + $skipSuffix)

if ($dockerDown -or $anyPanicOverall -or ($passedCount -ne $totalEval)) {
    exit 1
}
exit 0
