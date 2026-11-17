# Universal Windows Installer
param (
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\skipper"
)

$ErrorActionPreference = "Stop"
Write-Host "==> Installing Skipper CLI for Windows..." -ForegroundColor Cyan

if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$sourceExe = "$PSScriptRoot\..\bin\skipper-windows-amd64.exe"
if (-not (Test-Path $sourceExe)) {
    $sourceExe = "$PSScriptRoot\..\bin\skipper.exe"
}

if (Test-Path $sourceExe) {
    Copy-Item $sourceExe "$InstallDir\skipper.exe" -Force
} else {
    Write-Host "==> Compiling skipper.exe from source..."
    & go build -o "$InstallDir\skipper.exe" "$PSScriptRoot\..\cmd\skipper"
}

# Add to User PATH if missing
$userPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$InstallDir*") {
    [System.Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
    Write-Host "==> Added $InstallDir to user PATH." -ForegroundColor Green
}

Write-Host "==> Installation complete! Run 'skipper --help' to get started." -ForegroundColor Green
