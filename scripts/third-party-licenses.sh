#!/usr/bin/env bash
# Generate third_party_licenses/ — the license text of every Go module compiled
# into the tarjan binary — so release archives ship the attribution their
# licenses (MIT / BSD / Apache-2.0) require of binary distributions.
#
# Zero external tools on purpose: the module list comes from `go list -deps`
# (exactly what links into the binary — test-only deps are excluded), and the
# texts come from the local module cache. Run by goreleaser before each release
# (see .goreleaser.yaml) or by hand with: make licenses
set -euo pipefail

out="${1:-third_party_licenses}"
rm -rf "$out"
mkdir -p "$out"

go mod download

# Union over every OS we ship (see .goreleaser.yaml): platform-specific deps
# (e.g. windows-only console libraries) link into some binaries but would be
# invisible to a single-platform `go list`.
for goos in linux darwin windows; do
  GOOS="$goos" go list -deps \
    -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' .
done |
  sort -u |
  while read -r mod dir; do
    found=""
    for f in LICENSE LICENSE.md LICENSE.txt COPYING NOTICE; do
      if [ -f "$dir/$f" ]; then
        mkdir -p "$out/$mod"
        # install (not cp): files in the module cache are read-only.
        install -m 0644 "$dir/$f" "$out/$mod/$f"
        found=1
      fi
    done
    # A module with no license text may not be redistributable — fail loudly
    # instead of shipping an archive that silently omits an attribution.
    if [ -z "$found" ]; then
      echo "error: no license file found for $mod in $dir" >&2
      exit 1
    fi
  done

echo "wrote $(find "$out" -type f | wc -l | tr -d ' ') license files for $(find "$out" -mindepth 1 -maxdepth 4 -type d -name '*@*' | wc -l | tr -d ' ') modules to $out/"
