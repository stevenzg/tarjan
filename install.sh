#!/usr/bin/env bash
# tarjan installer — downloads the right prebuilt binary from GitHub Releases
# and installs it onto your PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/stevenzg/tarjan/main/install.sh | bash
#
# No GitHub credential is required — tarjan is a public repo. The installer
# uses the gh CLI when it is available and authenticated; otherwise it calls
# the GitHub API with curl. A token from GH_TOKEN / GITHUB_TOKEN is attached
# when present (it raises GitHub's API rate limits, useful on shared CI).
#
# Environment overrides:
#   VERSION      install a specific tag (default: latest), e.g. VERSION=v0.5.0
#   BIN_DIR      install location (default: $HOME/.local/bin)
#   GH_TOKEN     optional GitHub token (or GITHUB_TOKEN) — raises API rate limits
set -euo pipefail

REPO="stevenzg/tarjan"
BINARY="tarjan"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"

info()  { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn()  { printf '\033[1;33mwarning:\033[0m %s\n' "$1" >&2; }
die()   { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"; }
need uname
need tar

have() { command -v "$1" >/dev/null 2>&1; }

# --- authentication (optional) ----------------------------------------------
# tarjan is a public repo, so nothing here is required. Prefer the gh CLI when
# it is installed and authenticated; otherwise drive the GitHub API with curl,
# attaching a token from the environment when one is set (higher rate limits).
use_gh=""
if have gh && gh auth token >/dev/null 2>&1; then
  use_gh=1
fi

token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"

# Without gh, the GitHub API is driven over curl.
if [ -z "$use_gh" ]; then need curl; fi

# api_curl <curl args...> — curl with the Authorization header attached only
# when a token is set. The empty-token branch avoids bash-3.2 (macOS) pitfalls
# with empty arrays under `set -u`.
api_curl() {
  if [ -n "$token" ]; then
    curl -fsSL -H "Authorization: Bearer $token" "$@"
  else
    curl -fsSL "$@"
  fi
}

# api_json <url> — GET a GitHub API resource as JSON (same-host, no redirect).
# Only reached on the non-gh path, which already required curl above.
api_json() {
  api_curl -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" "$1"
}

# api_asset <asset-api-url> <outfile> — download a release asset's bytes. The
# API redirects to a signed CDN URL on a different host; curl drops the
# Authorization header on that hop (which the CDN requires), so this path uses
# curl.
api_asset() {
  api_curl -H "Accept: application/octet-stream" -o "$2" "$1"
}

# asset_url <name> — extract an asset's API url from the release JSON in $rel.
# The API returns pretty-printed JSON, so each asset's "url" and "name" are on
# separate lines with "url" first; awk tracks the most recent assets url and
# prints it when the matching name line appears. match()/substr() keep this to
# portable POSIX awk (no gensub/backreferences).
asset_url() {
  printf '%s' "$rel" | awk -v want="$1" '
    { if (match($0, /https:[^"]*\/releases\/assets\/[0-9]+/)) u = substr($0, RSTART, RLENGTH) }
    index($0, "\"name\": \"" want "\"") { print u; exit }
  '
}

# --- detect platform -------------------------------------------------------
os=$(uname -s)
case "$os" in
  Darwin) os="darwin" ;;
  Linux)  os="linux" ;;
  *) die "unsupported OS: $os (use the Windows installer or download manually)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) die "unsupported architecture: $arch" ;;
esac

# --- resolve version -------------------------------------------------------
tag="${VERSION:-}"
if [ -z "$tag" ]; then
  info "Resolving latest release..."
  if [ -n "$use_gh" ]; then
    tag=$(gh release view --repo "$REPO" --json tagName --jq .tagName 2>/dev/null || true)
  else
    tag=$(api_json "https://api.github.com/repos/$REPO/releases/latest" \
          | grep -m1 '"tag_name"' \
          | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
  fi
  [ -n "$tag" ] || die "could not determine latest version (set VERSION=vX.Y.Z to override)"
fi
ver="${tag#v}"  # archive names use the bare version, e.g. 0.5.0

asset="${BINARY}_${ver}_${os}_${arch}.tar.gz"

# --- download --------------------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

info "Downloading $asset ($tag)..."
if [ -n "$use_gh" ]; then
  gh release download "$tag" --repo "$REPO" \
    --pattern "$asset" --pattern "checksums.txt" --dir "$tmp" --clobber \
    || die "download failed for $tag (does the release exist?)"
else
  rel=$(api_json "https://api.github.com/repos/$REPO/releases/tags/$tag") \
    || die "could not fetch release $tag"
  aurl=$(asset_url "$asset") || true
  sums_url=$(asset_url "checksums.txt") || true
  [ -n "$aurl" ] || die "release $tag has no asset $asset"
  api_asset "$aurl" "$tmp/$asset" || die "download failed: $asset"
  if [ -n "$sums_url" ]; then
    api_asset "$sums_url" "$tmp/checksums.txt" 2>/dev/null || true
  fi
fi

# --- verify checksum -------------------------------------------------------
# Verification is mandatory: a network attacker who makes only the checksums
# fetch fail must not be able to downgrade the install to "no integrity check".
[ -f "$tmp/checksums.txt" ] || die "could not fetch checksums.txt for $tag — refusing to install an unverified binary"
if have sha256sum; then SUM="sha256sum";
elif have shasum; then SUM="shasum -a 256";
else die "no sha256 tool (sha256sum or shasum) found — cannot verify the download; install one and retry"; fi
want=$(grep " $asset\$" "$tmp/checksums.txt" | awk '{print $1}')
got=$(cd "$tmp" && $SUM "$asset" | awk '{print $1}')
[ -n "$want" ] && [ "$want" = "$got" ] || die "checksum mismatch for $asset"
info "Checksum verified."

# --- install ---------------------------------------------------------------
tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/$BINARY" ] || die "archive did not contain expected binary '$BINARY'"

mkdir -p "$BIN_DIR"
# Remove any existing binary first: overwriting a currently-running tarjan in
# place fails with ETXTBSY on Linux, whereas unlinking then writing a fresh file
# succeeds (the running process keeps its open inode).
install -m 0755 "$tmp/$BINARY" "$BIN_DIR/$BINARY" 2>/dev/null \
  || { rm -f "$BIN_DIR/$BINARY" && cp "$tmp/$BINARY" "$BIN_DIR/$BINARY" && chmod 0755 "$BIN_DIR/$BINARY"; }

info "Installed $BINARY $tag to $BIN_DIR/$BINARY"

# --- PATH guidance ---------------------------------------------------------
case ":$PATH:" in
  *":$BIN_DIR:"*) "$BIN_DIR/$BINARY" version 2>/dev/null || true ;;
  *)
    warn "$BIN_DIR is not on your PATH. Add it, e.g.:"
    printf '\n    echo '\''export PATH="%s:$PATH"'\'' >> ~/.profile && source ~/.profile\n\n' "$BIN_DIR"
    ;;
esac
