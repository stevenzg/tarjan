#!/usr/bin/env bash
# Run the test suite with coverage and fail if total statement coverage falls
# below MIN_COVERAGE (default 66). Set GOTEST_FLAGS (e.g. "-race") to pass extra
# flags to `go test`. The per-package breakdown printed by `go test ./...` is
# left on stdout for visibility.
set -euo pipefail

MIN_COVERAGE="${MIN_COVERAGE:-66}"
profile="$(mktemp)"
trap 'rm -f "$profile"' EXIT

# Cover every package except the root module wrapper (main.go just calls
# cmd.Execute — it carries no logic worth measuring, and a test-less package
# trips the coverage tooling on some toolchains).
module="$(go list -m)"
pkgs="$(go list ./... | grep -vx "$module" | tr '\n' ' ')"

echo "==> go test ${GOTEST_FLAGS:-} -covermode=atomic  (min coverage: ${MIN_COVERAGE}%)"
# GOTEST_FLAGS and pkgs are intentionally unquoted so they word-split.
# shellcheck disable=SC2086
go test ${GOTEST_FLAGS:-} -covermode=atomic -coverprofile="$profile" $pkgs

total="$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%","",$3); print $3}')"
echo
echo "==> total coverage: ${total}%"

if awk -v t="$total" -v m="$MIN_COVERAGE" 'BEGIN { exit !(t+0 < m+0) }'; then
	echo "FAIL: total coverage ${total}% is below the ${MIN_COVERAGE}% threshold" >&2
	exit 1
fi
echo "OK: total coverage ${total}% meets the ${MIN_COVERAGE}% threshold"
