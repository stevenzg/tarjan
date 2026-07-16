# tarjan installer for Windows PowerShell.
#
#   irm https://raw.githubusercontent.com/stevenzg/tarjan/main/install.ps1 | iex
#
# No GitHub credential is required — tarjan is a public repo. A token from
# GH_TOKEN / GITHUB_TOKEN (or the gh CLI) is attached when present; it raises
# GitHub's API rate limits, which helps on shared CI runners.
#
# Environment overrides:
#   $env:VERSION      install a specific tag (default: latest), e.g. v0.1.0
#   $env:BIN_DIR      install location (default: %LOCALAPPDATA%\tarjan\bin)
#   $env:GH_TOKEN     optional GitHub token (or GITHUB_TOKEN) — raises API rate limits
$ErrorActionPreference = "Stop"

$repo   = "stevenzg/tarjan"
$binary = "tarjan"
$binDir = if ($env:BIN_DIR) { $env:BIN_DIR } else { Join-Path $env:LOCALAPPDATA "tarjan\bin" }

# --- authentication (optional) ----------------------------------------------
# tarjan is a public repo, so no credential is needed. A token from the
# environment (or the gh CLI) is attached when present — it raises GitHub's
# API rate limits.
$token = if ($env:GH_TOKEN) { $env:GH_TOKEN } elseif ($env:GITHUB_TOKEN) { $env:GITHUB_TOKEN } else { "" }
if (-not $token -and (Get-Command gh -ErrorAction SilentlyContinue)) {
  $token = (gh auth token 2>$null | Out-String).Trim()
}
$apiHeaders = @{
  Accept                 = "application/vnd.github+json"
  "X-GitHub-Api-Version" = "2022-11-28"
}
$dlHeaders = @{ Accept = "application/octet-stream" }
if ($token) {
  $apiHeaders.Authorization = "Bearer $token"
  $dlHeaders.Authorization  = "Bearer $token"
}

# --- detect architecture ---------------------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

# --- resolve version + release ---------------------------------------------
$tag = $env:VERSION
if ($tag) {
  $relUrl = "https://api.github.com/repos/$repo/releases/tags/$tag"
} else {
  Write-Host "==> Resolving latest release..."
  $relUrl = "https://api.github.com/repos/$repo/releases/latest"
}
$rel = Invoke-RestMethod -Uri $relUrl -Headers $apiHeaders
$tag = $rel.tag_name
if (-not $tag) { throw "could not determine release (set `$env:VERSION)" }
$ver = $tag.TrimStart("v")

$asset = "${binary}_${ver}_windows_${arch}.zip"

# Resolve the asset API URLs (the API endpoint, not the public download URL).
$assetObj = $rel.assets | Where-Object { $_.name -eq $asset } | Select-Object -First 1
$sumsObj  = $rel.assets | Where-Object { $_.name -eq "checksums.txt" } | Select-Object -First 1
if (-not $assetObj) { throw "release $tag has no asset $asset" }
if (-not $sumsObj)  { throw "release $tag has no checksums.txt — refusing to install an unverified binary" }

# --- download & verify -----------------------------------------------------
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "==> Downloading $asset ($tag)..."
  Invoke-WebRequest -Uri $assetObj.url -Headers $dlHeaders -OutFile (Join-Path $tmp $asset)
  Invoke-WebRequest -Uri $sumsObj.url  -Headers $dlHeaders -OutFile (Join-Path $tmp "checksums.txt")

  # Verification is MANDATORY. A failed checksum fetch or an actual mismatch must
  # abort the install — never fall through to installing an unverified binary.
  $want = (Select-String -Path (Join-Path $tmp "checksums.txt") -Pattern ([regex]::Escape($asset)) |
           Select-Object -First 1).Line.Split(" ")[0]
  $got  = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
  if (-not $want) { throw "no checksum listed for $asset — refusing to install an unverified binary" }
  if ($want -ne $got) { throw "checksum mismatch for $asset (want $want, got $got)" }
  Write-Host "==> Checksum verified."

  # --- install -------------------------------------------------------------
  Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force
  $exe = Join-Path $tmp "$binary.exe"
  if (-not (Test-Path $exe)) { throw "archive did not contain $binary.exe" }

  New-Item -ItemType Directory -Force -Path $binDir | Out-Null
  Copy-Item $exe (Join-Path $binDir "$binary.exe") -Force
  Write-Host "==> Installed $binary $tag to $binDir\$binary.exe"
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# --- PATH guidance ---------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$binDir*") {
  [Environment]::SetEnvironmentVariable("Path", "$userPath;$binDir", "User")
  Write-Host "==> Added $binDir to your user PATH. Open a new terminal to use '$binary'."
} else {
  & (Join-Path $binDir "$binary.exe") version
}
