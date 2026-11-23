# Universal Windows Installer
param (
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\skipper",
    [string]$Version = "",
    [switch]$SkipPathUpdate
)

$ErrorActionPreference = "Stop"
Write-Host "==> Installing Skipper CLI for Windows..." -ForegroundColor Cyan

if ([string]::IsNullOrWhiteSpace($Version) -and -not [string]::IsNullOrWhiteSpace($env:SKIPPER_VERSION)) {
    $Version = $env:SKIPPER_VERSION
}

if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$repo = if (-not [string]::IsNullOrWhiteSpace($env:SKIPPER_REPO)) { $env:SKIPPER_REPO } else { "sensi012/skipper" }
$asset = "skipper-windows-amd64.exe"
$targetExe = Join-Path $InstallDir "skipper.exe"

$repoRoot = ""
if (-not [string]::IsNullOrWhiteSpace($PSScriptRoot)) {
    $candidate = Split-Path -Parent $PSScriptRoot
    $candidateMain = Join-Path (Join-Path (Join-Path $candidate "cmd") "skipper") "main.go"
    if (Test-Path $candidateMain) {
        $repoRoot = $candidate
    }
}
if ([string]::IsNullOrWhiteSpace($repoRoot)) {
    $cwd = (Get-Location).Path
    $cwdMain = Join-Path (Join-Path (Join-Path $cwd "cmd") "skipper") "main.go"
    if (Test-Path $cwdMain) {
        $repoRoot = $cwd
    }
}

$sourceExe = ""
if (-not [string]::IsNullOrWhiteSpace($repoRoot) -and [string]::IsNullOrWhiteSpace($Version)) {
    $binDir = Join-Path $repoRoot "bin"
    $archBin = Join-Path $binDir $asset
    $plainBin = Join-Path $binDir "skipper.exe"
    if (Test-Path $archBin) {
        $sourceExe = $archBin
    } elseif (Test-Path $plainBin) {
        $sourceExe = $plainBin
    }
}

if (-not [string]::IsNullOrWhiteSpace($sourceExe)) {
    Write-Host "==> Installing from local binary ($sourceExe)..."
    Copy-Item $sourceExe $targetExe -Force
} elseif (-not [string]::IsNullOrWhiteSpace($repoRoot) -and [string]::IsNullOrWhiteSpace($Version) -and (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "==> Compiling skipper.exe from source..."
    $cmdPkg = Join-Path (Join-Path $repoRoot "cmd") "skipper"
    Push-Location $repoRoot
    try {
        & go build -o $targetExe $cmdPkg
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
} else {
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    } catch {
    }

    $baseUrl = ""
    if (-not [string]::IsNullOrWhiteSpace($Version)) {
        $baseUrl = "https://github.com/$repo/releases/download/$Version"
    } else {
        $latestDownload = "https://github.com/$repo/releases/latest/download"
        try {
            $null = Invoke-WebRequest -Method Head -Uri "$latestDownload/$asset" -UseBasicParsing -ErrorAction Stop
            $baseUrl = $latestDownload
        } catch {
            $releases = @(Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases" -Headers @{ "User-Agent" = "skipper-installer" } -ErrorAction Stop)
            if ($releases.Count -eq 0 -or [string]::IsNullOrWhiteSpace([string]$releases[0].tag_name)) {
                throw "Could not resolve any GitHub Release tag for $repo."
            }
            $tag = [string]$releases[0].tag_name
            $baseUrl = "https://github.com/$repo/releases/download/$tag"
        }
    }

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("skipper-install-" + [System.Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
    try {
        $tmpAsset = Join-Path $tmpDir $asset
        $tmpChecksums = Join-Path $tmpDir "checksums.txt"

        Write-Host "==> Downloading $asset from $baseUrl..."
        Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$asset" -OutFile $tmpAsset
        Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/checksums.txt" -OutFile $tmpChecksums

        $expectedHash = ""
        $lines = [System.IO.File]::ReadAllLines($tmpChecksums)
        foreach ($line in $lines) {
            $trimmed = $line.Trim()
            if ($trimmed -match '^([A-Fa-f0-9]{64})\s+\*?(.+)$') {
                $hashVal = $Matches[1].ToLowerInvariant()
                $fileName = $Matches[2].Trim()
                if ($fileName -eq $asset) {
                    $expectedHash = $hashVal
                    break
                }
            }
        }
        if ([string]::IsNullOrWhiteSpace($expectedHash)) {
            throw "Checksum entry for '$asset' not found in checksums.txt"
        }

        $actualHash = (Get-FileHash -Path $tmpAsset -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash) {
            throw "SHA256 mismatch for '$asset' (expected $expectedHash, got $actualHash)"
        }
        Write-Host "==> Verified SHA256 checksum for $asset." -ForegroundColor Green

        Copy-Item $tmpAsset $targetExe -Force
    } finally {
        if (Test-Path $tmpDir) {
            Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
        }
    }
}

if (-not $SkipPathUpdate) {
    $userPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$InstallDir*") {
        $newPath = if ([string]::IsNullOrWhiteSpace($userPath)) { $InstallDir } else { "$userPath;$InstallDir" }
        [System.Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        Write-Host "==> Added $InstallDir to user PATH." -ForegroundColor Green
    }
}
$env:Path = $env:Path + ";$InstallDir"

Write-Host "==> Installation complete! Run 'skipper --help' to get started." -ForegroundColor Green
Write-Host "    (Note: If you have other PowerShell terminal windows open, restart them to refresh PATH)" -ForegroundColor Yellow
