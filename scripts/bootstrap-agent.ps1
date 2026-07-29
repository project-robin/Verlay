param(
    [string]$InstallRoot = (Join-Path $env:LOCALAPPDATA "Verlay"),
    [string]$SkillHome = (Join-Path $HOME ".agents\skills"),
    [ValidateSet("auto", "checkout", "release")]
    [string]$Source = "auto",
    [string]$Repository = "project-robin/Verlay",
    [string]$Version = "latest",
    [ValidateSet("auto", "copy", "skills-cli")]
    [string]$SkillMethod = "auto",
    [string[]]$Agent = @(),
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    throw "Verlay bootstrap failed: $Message"
}

if ($env:OS -ne "Windows_NT") {
    Fail "bootstrap-agent.ps1 supports Windows only"
}

$TemporaryCheckout = $null
$SourceRoot = if ($PSScriptRoot) { Split-Path -Parent $PSScriptRoot } else { "" }
$LocalCheckout = $false
if ($SourceRoot) {
    $LocalCheckout = (
        (Test-Path (Join-Path $SourceRoot "go.mod")) -and
        (Test-Path (Join-Path $SourceRoot "cmd\verlay")) -and
        (Test-Path (Join-Path $SourceRoot "skills\verlay\SKILL.md"))
    )
}
if (-not $LocalCheckout) {
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
        Fail "Git is required when bootstrap-agent.ps1 is run outside a checkout"
    }
    $TemporaryCheckout = Join-Path ([System.IO.Path]::GetTempPath()) ("verlay-bootstrap-" + [guid]::NewGuid())
    $SourceRoot = Join-Path $TemporaryCheckout "repo"
    New-Item -ItemType Directory -Force $TemporaryCheckout | Out-Null
    if ($Version -eq "latest") {
        & git clone --quiet --depth 1 "https://github.com/$Repository.git" $SourceRoot
    } else {
        & git clone --quiet --depth 1 --branch $Version "https://github.com/$Repository.git" $SourceRoot
    }
    if ($LASTEXITCODE -ne 0) {
        Fail "could not download the Verlay repository"
    }
}
if ($Source -eq "auto") {
    $Source = if ($LocalCheckout) { "checkout" } else { "release" }
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
    if ($Source -eq "checkout") {
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
            Fail "Go is required for a checkout installation"
        }
        $env:GOTOOLCHAIN = "auto"
        Push-Location $SourceRoot
        try {
            & go build -trimpath -ldflags "-s -w" -o $BuiltBinary ./cmd/verlay
            if ($LASTEXITCODE -ne 0) {
                Fail "the Windows runtime build failed; do not edit go.mod or downgrade dependencies"
            }
        } finally {
            Pop-Location
        }
    } else {
        if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne
            [System.Runtime.InteropServices.Architecture]::X64) {
            Fail "the Windows beta release currently supports x64 only"
        }
        $Asset = "verlay-windows-amd64.exe"
        if ($Version -eq "latest") {
            $ReleaseBase = "https://github.com/$Repository/releases/latest/download"
        } else {
            $ReleaseBase = "https://github.com/$Repository/releases/download/$Version"
        }
        $Checksums = Join-Path $BuildDir "verlay-checksums.txt"
        Invoke-WebRequest -UseBasicParsing "$ReleaseBase/$Asset" -OutFile $BuiltBinary
        Invoke-WebRequest -UseBasicParsing "$ReleaseBase/verlay-checksums.txt" -OutFile $Checksums
        $ChecksumLine = Get-Content $Checksums |
            Where-Object { $_ -match "^[0-9a-fA-F]{64}\s+\*?$([regex]::Escape($Asset))$" } |
            Select-Object -First 1
        if (-not $ChecksumLine) {
            Fail "the release checksum file does not contain $Asset"
        }
        $Expected = ($ChecksumLine -split "\s+")[0].ToLowerInvariant()
        $Actual = (Get-FileHash -Algorithm SHA256 $BuiltBinary).Hash.ToLowerInvariant()
        if ($Expected -ne $Actual) {
            Fail "Verlay checksum verification failed"
        }
    }

    & $BuiltBinary --json version | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Fail "the downloaded Verlay binary did not start"
    }
    & $BuiltBinary --json schema | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Fail "the downloaded Verlay binary failed schema validation"
    }

    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Get-Process verlayd -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -eq $Verlayd } |
        Stop-Process -Force
    Copy-Item -Force $BuiltBinary $Verlay
    Copy-Item -Force $BuiltBinary $Verlayd

    New-Item -ItemType Directory -Force (Join-Path $CanonicalSkill "references") | Out-Null
    Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\SKILL.md") (Join-Path $CanonicalSkill "SKILL.md")
    Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\references\command-reference.md") (Join-Path $CanonicalSkill "references\command-reference.md")
    Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\references\security.md") (Join-Path $CanonicalSkill "references\security.md")

    if ($Agent.Count -eq 0) {
        if (Get-Command codex -ErrorAction SilentlyContinue) { $Agent += "codex" }
        if (Get-Command pi -ErrorAction SilentlyContinue) { $Agent += "pi" }
        if (Get-Command claude -ErrorAction SilentlyContinue) { $Agent += "claude-code" }
        if (Get-Command cursor -ErrorAction SilentlyContinue) { $Agent += "cursor" }
        if (Get-Command opencode -ErrorAction SilentlyContinue) { $Agent += "opencode" }
        if (Get-Command copilot -ErrorAction SilentlyContinue) { $Agent += "github-copilot" }
        if (Get-Command gemini -ErrorAction SilentlyContinue) { $Agent += "gemini-cli" }
    }

    $UseSkillsCLI = $false
    if ($SkillMethod -eq "skills-cli") {
        if (-not (Get-Command npx -ErrorAction SilentlyContinue)) {
            Fail "npx is required for -SkillMethod skills-cli"
        }
        $UseSkillsCLI = $true
    } elseif ($SkillMethod -eq "auto" -and (Get-Command npx -ErrorAction SilentlyContinue)) {
        $UseSkillsCLI = $true
    }
    if ($UseSkillsCLI) {
        $SkillsArgs = @("--yes", "skills", "add", $SourceRoot, "--skill", "verlay", "--global", "--copy", "--yes")
        foreach ($HostName in $Agent) {
            $SkillsArgs += @("--agent", $HostName)
        }
        & npx @SkillsArgs
        if ($LASTEXITCODE -ne 0) {
            if ($SkillMethod -eq "skills-cli") {
                Fail "the Skills CLI could not register Verlay"
            }
            Write-Warning "Skills CLI registration failed; the canonical skill remains at $CanonicalSkill"
        }
    } elseif ($SkillMethod -ne "copy" -and $Agent.Count -gt 0) {
        Write-Warning "Skills CLI unavailable; known detected hosts will receive direct copies from $CanonicalSkill"
    }

    foreach ($HostName in $Agent) {
        $HostSkill = switch ($HostName) {
            "codex" { Join-Path $HOME ".codex\skills\verlay" }
            "pi" { Join-Path $HOME ".pi\agent\skills\verlay" }
            "claude-code" { Join-Path $HOME ".claude\skills\verlay" }
            "cursor" { Join-Path $HOME ".cursor\skills\verlay" }
            "opencode" { Join-Path $HOME ".config\opencode\skills\verlay" }
            "github-copilot" { Join-Path $HOME ".copilot\skills\verlay" }
            "gemini-cli" { Join-Path $HOME ".gemini\skills\verlay" }
            default { $null }
        }
        if ($HostSkill) {
            New-Item -ItemType Directory -Force (Join-Path $HostSkill "references") | Out-Null
            Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\SKILL.md") (Join-Path $HostSkill "SKILL.md")
            Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\references\command-reference.md") (Join-Path $HostSkill "references\command-reference.md")
            Copy-Item -Force (Join-Path $SourceRoot "skills\verlay\references\security.md") (Join-Path $HostSkill "references\security.md")
        }
    }

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

    Push-Location ([System.IO.Path]::GetTempPath())
    try {
        $Resolved = Get-Command verlay -CommandType Application -ErrorAction Stop
        if ($Resolved.Source -ne $Verlay) {
            Fail "verlay resolves to an unexpected executable: $($Resolved.Source)"
        }
        & verlay --json version | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Fail "verlay was not usable outside its installation directory"
        }
    } finally {
        Pop-Location
    }
} finally {
    Remove-Item -Recurse -Force $BuildDir -ErrorAction SilentlyContinue
    if ($TemporaryCheckout) {
        Remove-Item -Recurse -Force $TemporaryCheckout -ErrorAction SilentlyContinue
    }
}

Write-Host ""
Write-Host "Verlay bootstrap complete."
Write-Host "CLI: $Verlay"
Write-Host "Daemon: $Verlayd"
if (-not $NoStart) {
    Write-Host "Background task: $TaskName (starts at login)"
}
Write-Host "Agent Skill: $CanonicalSkill"
Write-Host "Start a new agent session if it does not reload PATH or skills automatically."
Write-Host "Security: no device was paired or authorized."
