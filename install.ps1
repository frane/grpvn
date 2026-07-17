# grpvn installer for Windows PowerShell.
#
# Downloads the release binary for this machine, verifies its sha256
# against the release's checksums.txt, and installs it. Falls back to
# `go install` when no prebuilt binary fits and a Go toolchain is on PATH.
#
# Usage:
#   irm https://raw.githubusercontent.com/frane/grpvn/main/install.ps1 | iex
#   # pin a version / bin dir:
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/frane/grpvn/main/install.ps1))) -Version v0.6.0 -BinDir $HOME\bin

[CmdletBinding()]
param(
    [string]$Version = "latest",
    [string]$BinDir = ""
)

$ErrorActionPreference = "Stop"
$Repo = "frane/grpvn"

function Say($m) { Write-Host "grpvn-install: $m" }
function Fail($m) { Write-Error "grpvn-install: $m"; exit 1 }

# Only windows/amd64 is published today; other arches fall through to go install.
$target = $null
if ($env:PROCESSOR_ARCHITECTURE -eq "AMD64") { $target = "windows_x86_64" }
else { Say "no prebuilt binary for arch $($env:PROCESSOR_ARCHITECTURE); will try go install" }

if (-not $BinDir) {
    $candidate = "$HOME\bin", "$HOME\.local\bin" |
        Where-Object { Test-Path $_ } | Select-Object -First 1
    if (-not $candidate) { $candidate = "$HOME\bin" }
    $BinDir = $candidate
}
if (-not (Test-Path $BinDir)) { New-Item -ItemType Directory -Force -Path $BinDir | Out-Null }

function Install-FromRelease {
    if (-not $target) { return $false }
    $ver = $Version
    if ($ver -eq "latest") {
        $rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
        $ver = $rel.tag_name
        if (-not $ver) { return $false }
    }
    $bare = $ver -replace '^v', ''
    $asset = "grpvn_${bare}_$target.zip"
    $base = "https://github.com/$Repo/releases/download/$ver"
    Say "downloading $asset"
    $tmp = Join-Path $env:TEMP ("grpvn-install-" + [System.Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        $zip = Join-Path $tmp $asset
        Invoke-WebRequest -Uri "$base/$asset" -OutFile $zip -UseBasicParsing
        # Same contract as install.sh: refuse anything that fails checksum.
        $sums = (Invoke-WebRequest -Uri "$base/checksums.txt" -UseBasicParsing).Content
        $line = $sums -split "`n" | Where-Object { $_ -match [regex]::Escape($asset) } | Select-Object -First 1
        if (-not $line) { Fail "no checksum for $asset in checksums.txt" }
        $want = ($line -split '\s+')[0].ToLower()
        $got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
        if ($want -ne $got) { Fail "sha256 mismatch for $asset (want $want, got $got)" }
        Expand-Archive -Path $zip -DestinationPath $tmp -Force
        $exe = Get-ChildItem -Path $tmp -Filter "grpvn.exe" -Recurse | Select-Object -First 1
        if (-not $exe) { Fail "grpvn.exe missing from $asset" }
        Copy-Item $exe.FullName (Join-Path $BinDir "grpvn.exe") -Force
        Say "grpvn $ver installed to $BinDir\grpvn.exe"
        return $true
    }
    finally { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
}

function Install-FromGo {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { return $false }
    Say "building with go install (this can take a minute)"
    $env:GOBIN = $BinDir
    go install "github.com/frane/grpvn/cmd/grpvn@$Version" 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) { return $false }
    Say "grpvn installed to $BinDir\grpvn.exe (from source)"
    return $true
}

if (-not (Install-FromRelease)) {
    if (-not (Install-FromGo)) {
        Fail "could not install: no matching release asset and no Go toolchain on PATH"
    }
}

$onPath = ($env:PATH -split ';') -contains $BinDir
if (-not $onPath) {
    Say "note: $BinDir is not on PATH. Add it, e.g.:"
    Say "  [Environment]::SetEnvironmentVariable('Path', `$env:Path + ';$BinDir', 'User')"
}
Say "next: run 'grpvn skill install' to wire your agent runtimes"
