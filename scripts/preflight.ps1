# scripts/preflight.ps1
#
# Local mirror of .github/workflows/ci.yml — run this before tagging a
# release so lint/vet/vuln/gosec failures show up locally instead of as
# a red CI badge on a freshly published tag.
#
# Personal maintainer tool; not advertised in README or ROADMAP.
#
# Usage (from repo root):
#   pwsh scripts/preflight.ps1            # full preflight
#   pwsh scripts/preflight.ps1 -Fast      # skip govulncheck + gosec
#   pwsh scripts/preflight.ps1 -SkipRace  # skip -race tests (no cgo locally)
#
# The script installs golangci-lint, govulncheck, and gosec into
# $env:GOBIN (or %USERPROFILE%\go\bin) on first run; subsequent runs
# reuse the installed binaries. It bumps versions only when CI's
# pinned version drifts — keep this script and ci.yml in lockstep.

[CmdletBinding()]
param(
    [switch] $Fast,
    [switch] $SkipRace
)

$ErrorActionPreference = "Stop"

# Modules linted by CI. Keep this list in sync with GALDOR_MODULES in
# .github/workflows/ci.yml and the lint/govulncheck/gosec matrices.
$Modules = @(
    ".",
    "providers/anthropic",
    "providers/bedrock",
    "providers/google",
    "providers/openai",
    "providerset",
    "memory/sqlite",
    "memory/pgvector",
    "memory/qdrant"
)
$ModulesWithExamples = $Modules + @("examples")

# Tool versions pinned to whatever CI is using. Update both places
# together when bumping.
$GolangciLintVersion = "v2.12.2"
$GovulncheckVersion  = "latest"
$GosecVersion        = "latest"

# Resolve the Go install. Windows users often don't have it on PATH.
$GoExe = (Get-Command go -ErrorAction SilentlyContinue)?.Source
if (-not $GoExe) {
    foreach ($p in @(
        "$env:ProgramFiles\Go\bin\go.exe",
        "$env:LOCALAPPDATA\Programs\Go\bin\go.exe",
        "C:\Go\bin\go.exe"
    )) {
        if (Test-Path $p) { $GoExe = $p; break }
    }
}
if (-not $GoExe) {
    throw "go not found in PATH or common install locations"
}

# Where `go install` puts binaries.
$GoBin = (& $GoExe env GOBIN).Trim()
if (-not $GoBin) {
    $GoPath = (& $GoExe env GOPATH).Trim()
    $GoBin = Join-Path $GoPath "bin"
}

function Require-Tool {
    param(
        [string] $Name,
        [string] $InstallPath,
        [string] $InstallVersion
    )
    $exe = Join-Path $GoBin "$Name.exe"
    if (-not (Test-Path $exe)) {
        Write-Host "  installing $Name@$InstallVersion ..." -ForegroundColor DarkGray
        & $GoExe install "$InstallPath@$InstallVersion"
        if ($LASTEXITCODE -ne 0) { throw "failed to install $Name" }
    }
    return $exe
}

function Section {
    param([string] $Title)
    Write-Host ""
    Write-Host "=== $Title ===" -ForegroundColor Cyan
}

function Run-In-Module {
    param(
        [string] $Module,
        [scriptblock] $Action
    )
    Push-Location $Module
    try { & $Action }
    finally { Pop-Location }
}

$StartTime = Get-Date
$Failures = @()

# 1. go mod tidy verification (CI does this on ubuntu only; mirror it).
Section "go mod tidy verification"
foreach ($m in $ModulesWithExamples) {
    Write-Host "  $m" -ForegroundColor DarkGray
    Run-In-Module $m {
        & $GoExe mod tidy
        $changed = git status --porcelain go.mod go.sum 2>$null
        if ($changed) {
            $script:Failures += "[$m] go.mod or go.sum is dirty after 'go mod tidy'"
            Write-Host "    DIRTY: $changed" -ForegroundColor Red
        }
    }
}

# 2. Build workspace.
Section "go build ./..."
& $GoExe build ./...
if ($LASTEXITCODE -ne 0) { $Failures += "go build (workspace)" }

# 3. Vet workspace.
Section "go vet ./..."
& $GoExe vet ./...
if ($LASTEXITCODE -ne 0) { $Failures += "go vet (workspace)" }

# 4. Tests.
Section "go test"
$testArgs = @("test", "-coverprofile=coverage.txt", "-covermode=atomic")
if (-not $SkipRace) {
    if (-not $env:CGO_ENABLED) { $env:CGO_ENABLED = "1" }
    $gcc = (Get-Command gcc -ErrorAction SilentlyContinue)
    if (-not $gcc) {
        Write-Host "  gcc not found; falling back to -count=1 without -race" -ForegroundColor Yellow
        Write-Host "  (CI runs -race on Linux/macOS; install MinGW or use -SkipRace to silence)" -ForegroundColor Yellow
        $testArgs += "-count=1"
    } else {
        $testArgs += "-race"
    }
} else {
    $testArgs += "-count=1"
}
$testArgs += "./..."
& $GoExe @testArgs
if ($LASTEXITCODE -ne 0) { $Failures += "go test (workspace)" }

# 5. golangci-lint per module (this is the one that bit us on v0.2.0).
Section "golangci-lint $GolangciLintVersion"
$Lint = Require-Tool "golangci-lint" "github.com/golangci/golangci-lint/v2/cmd/golangci-lint" $GolangciLintVersion
$ConfigPath = Join-Path (Get-Location) ".golangci.yml"
foreach ($m in $Modules) {
    Write-Host "  $m" -ForegroundColor DarkGray
    Run-In-Module $m {
        & $Lint run "--config=$ConfigPath"
        if ($LASTEXITCODE -ne 0) { $script:Failures += "[$m] golangci-lint" }
    }
}

if (-not $Fast) {
    # 6. govulncheck per module.
    Section "govulncheck"
    $Vuln = Require-Tool "govulncheck" "golang.org/x/vuln/cmd/govulncheck" $GovulncheckVersion
    foreach ($m in $Modules) {
        Write-Host "  $m" -ForegroundColor DarkGray
        Run-In-Module $m {
            & $Vuln ./...
            if ($LASTEXITCODE -ne 0) { $script:Failures += "[$m] govulncheck" }
        }
    }

    # 7. gosec per module.
    Section "gosec"
    $Sec = Require-Tool "gosec" "github.com/securego/gosec/v2/cmd/gosec" $GosecVersion
    foreach ($m in $Modules) {
        Write-Host "  $m" -ForegroundColor DarkGray
        Run-In-Module $m {
            & $Sec ./...
            if ($LASTEXITCODE -ne 0) { $script:Failures += "[$m] gosec" }
        }
    }
} else {
    Write-Host ""
    Write-Host "(skipped govulncheck + gosec; pass without -Fast for full preflight)" -ForegroundColor DarkGray
}

# Summary.
$elapsed = (Get-Date) - $StartTime
Write-Host ""
Write-Host "=== preflight summary ===" -ForegroundColor Cyan
Write-Host ("elapsed: {0:mm}m{0:ss}s" -f $elapsed)
if ($Failures.Count -eq 0) {
    Write-Host "  ALL CHECKS PASSED" -ForegroundColor Green
    exit 0
} else {
    Write-Host "  FAILED:" -ForegroundColor Red
    foreach ($f in $Failures) { Write-Host "    - $f" -ForegroundColor Red }
    exit 1
}
