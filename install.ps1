# Install own-code from source on Windows: shallow-clone own-code and the
# ownframe sources it builds against, then build the binary into $PREFIX/bin.
# A sibling checkout is required because go.mod points at ../ownframe, but
# only its Go sources are checked out.
$ErrorActionPreference = "Stop"

$Repo = "https://github.com/chinmay-sawant/own-code.git"
$OwnframeRepo = "https://github.com/chinmay-sawant/ownframe.git"
$Branch = "master"
# Master commit pinned in go.mod. Keep in sync with go.mod.
$OwnframePin = "f2f079065cfa8e63a6a572fca9a4403e5f975e16"

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
  Write-Error "own-code: git not found; install git first"
  exit 1
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  Write-Error "own-code: go not found; install Go >= 1.26 first"
  exit 1
}

$goVersion = (go version) -join " "
if ($goVersion -match "go(\d+)\.(\d+)") {
  $major = [int]$Matches[1]
  $minor = [int]$Matches[2]
} else {
  Write-Error "own-code: could not parse go version: $goVersion"
  exit 1
}
if (-not ($major -gt 1 -or ($major -eq 1 -and $minor -ge 26))) {
  Write-Error "own-code: need go >= 1.26, found: $goVersion"
  exit 1
}

$Prefix = $env:PREFIX
if ([string]::IsNullOrEmpty($Prefix)) {
  $Prefix = Join-Path $HOME ".local"
}
$BinDir = Join-Path $Prefix "bin"
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("own-code-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
  git clone --depth 1 --branch $Branch $Repo (Join-Path $Tmp "own-code")
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  git clone --depth 1 --branch master --filter=blob:none --sparse $OwnframeRepo (Join-Path $Tmp "ownframe")
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  git -C (Join-Path $Tmp "ownframe") sparse-checkout set --no-cone '/*.go' '/go.mod' '/internal/'
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  $Head = (git -C (Join-Path $Tmp "ownframe") rev-parse HEAD).Trim()
  if ($Head -ne $OwnframePin) {
    git -C (Join-Path $Tmp "ownframe") fetch --depth 1 origin $OwnframePin
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    git -C (Join-Path $Tmp "ownframe") checkout $OwnframePin
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  }
  Push-Location (Join-Path $Tmp "own-code")
  try {
    go build -o own-code.exe ./cmd/own-code
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    Copy-Item (Join-Path $Tmp "own-code\own-code.exe") (Join-Path $BinDir "own-code.exe") -Force
  } finally {
    Pop-Location
  }
} finally {
  Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

Write-Host "Installed own-code to $BinDir\own-code.exe"
if (($env:PATH -split ";" | Where-Object { $_ -eq $BinDir }).Count -eq 0) {
  Write-Host "Add $BinDir to your PATH, e.g.:"
  Write-Host "  `$env:PATH = `"$BinDir;`$env:PATH`""
  Write-Host "To keep it permanently: Settings > System > About > Advanced system settings > Environment Variables."
}
