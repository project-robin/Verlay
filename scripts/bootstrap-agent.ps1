param(
    [string]$InstallRoot = (Join-Path $env:LOCALAPPDATA "Verlay"),
    [string]$SkillHome = (Join-Path $HOME ".agents\skills"),
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    throw "Verlay bootstrap failed: $Message"
}

if ($env:OS -ne "Windows_NT") {
    Fail "bootstrap-agent.ps1 supports Windows only"
}

$SourceRoot = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $SourceRoot "go.mod")) -or
    -not (Test-Path (Join-Path $SourceRoot "cmd\verlay"))) {
    Fail "run this script from a complete Verlay source checkout"
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail "Go is required for the unreleased Windows beta. Install Go, reopen PowerShell, and retry"
}

$env:GOTOOLCHAIN = "auto"
$GoVersion = (& go env GOVERSION).Trim()
if ($LASTEXITCODE -ne 0 -or $GoVersion -notmatch "^go(\d+)\.(\d+)") {
    Fail "could not determine the installed Go version"
}
if ([int]$Matches[1] -lt 1 -or ([int]$Matches[1] -eq 1 -and [int]$Matches[2] -lt 21)) {
    Fail "Go 1.21 or newer is required so Go can select the toolchain declared by Verlay"
}

$BinDir = Join-Path $InstallRoot "bin"
$Verlay = Join-Path $BinDir "verlay.exe"
$Verlayd = Join-Path $BinDir "verlayd.exe"
$TaskName = "Verlay"
$CanonicalSkill = Join-Path $SkillHome "verlay"
$BuildDir = Join-Path ([System.IO.Path]::GetTempPath()) ("verlay-build-" + [guid]::NewGuid())
$BuiltBinary = Join-Path $BuildDir "verlay.exe"

New-Item -ItemType Directory -Force $BinDir, $BuildDir | Out-Null
try {
    Push-Location $SourceRoot
    try {
        & go build -trimpath -ldflags "-s -w" -o $BuiltBinary ./cmd/verlay
        if ($LASTEXITCODE -ne 0) {
            Fail "the Windows runtime build failed; do not edit go.mod or downgrade dependencies"
        }
    } finally {
        Pop-Location
    }

    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Get-Process verlayd -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -eq $Verlayd } |
        Stop-Process -Force
    Copy-Item -Force $BuiltBinary $Verlay
    Copy-Item -Force $BuiltBinary $Verlayd
} finally {
    Remove-Item -Recurse -Force $BuildDir -ErrorAction SilentlyContinue
}

New-Item -ItemType Directory -Force (Join-Path $CanonicalSkill "references") | Out-Null
Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\SKILL.md") (Join-Path $CanonicalSkill "SKILL.md")
Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\references\command-reference.md") (Join-Path $CanonicalSkill "references\command-reference.md")
Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\references\security.md") (Join-Path $CanonicalSkill "references\security.md")

$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
$PathEntries = @($UserPath -split ";" | Where-Object { $_ })
if ($BinDir -notin $PathEntries) {
    [Environment]::SetEnvironmentVariable("Path", (($PathEntries + $BinDir) -join ";"), "User")
}
if ($BinDir -notin ($env:Path -split ";")) {
    $env:Path = "$BinDir;$env:Path"
}

& $Verlay --json version
if ($LASTEXITCODE -ne 0) {
    Fail "verlay.exe did not start"
}

if (-not $NoStart) {
    $Identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $Action = New-ScheduledTaskAction -Execute $Verlayd
    $Trigger = New-ScheduledTaskTrigger -AtLogOn -User $Identity
    $Principal = New-ScheduledTaskPrincipal -UserId $Identity -LogonType Interactive -RunLevel Limited
    $Settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -StartWhenAvailable `
        -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -MultipleInstances IgnoreNew
    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $Action `
        -Trigger $Trigger `
        -Principal $Principal `
        -Settings $Settings `
        -Description "Verlay secure device daemon" `
        -Force | Out-Null
    Start-ScheduledTask -TaskName $TaskName
    $Ready = $false
    for ($Attempt = 0; $Attempt -lt 50; $Attempt++) {
        Start-Sleep -Milliseconds 100
        & $Verlay --json status *> $null
        if ($LASTEXITCODE -eq 0) {
            $Ready = $true
            break
        }
    }
    if (-not $Ready) {
        Fail "verlayd did not become ready"
    }
    & $Verlay --json status
    & $Verlay --json doctor
}

Write-Host ""
Write-Host "Verlay bootstrap complete."
Write-Host "CLI: $Verlay"
Write-Host "Daemon: $Verlayd"
if (-not $NoStart) {
    Write-Host "Background task: $TaskName (starts at login)"
}
Write-Host "Agent Skill: $CanonicalSkill"
Write-Host "Security: no device was paired or authorized."
