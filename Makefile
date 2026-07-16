BINARY := tarjan
PKG := github.com/stevenzg/tarjan

# Inject version metadata into `tarjan version` from git (overridable at release).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/cmd.version=$(VERSION) \
	-X $(PKG)/cmd.commit=$(COMMIT) \
	-X $(PKG)/cmd.date=$(DATE)

# Minimum total statement coverage enforced by `make cover` (override on the CLI).
MIN_COVERAGE ?= 66

.PHONY: build install test vet fmt fmt-check lint check cover hooks clean dist snapshot

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

install:
	go install -ldflags "$(LDFLAGS)" .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Fail (non-zero) if anything is not gofmt-ed — used by CI.
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-ed:"; echo "$$unformatted"; exit 1; \
	fi

lint:
	golangci-lint run ./...

# Run the suite with the race detector and enforce the coverage floor.
cover:
	MIN_COVERAGE=$(MIN_COVERAGE) GOTEST_FLAGS=-race ./scripts/coverage.sh

# Everything a PR must pass, runnable locally.
check: fmt-check vet lint cover

# Install the local git pre-commit hook (zero extra dependencies).
hooks:
	git config core.hooksPath .githooks
	@echo "pre-commit hook enabled (bypass with: git commit --no-verify)"

clean:
	rm -rf bin dist

# Build local release artifacts. Delegates to goreleaser so the produced
# archives (names, checksums.txt, platforms) match what a real release ships —
# a hand-rolled cross-compile drifts from .goreleaser.yaml and yields binaries
# install.sh / selfupdate cannot consume.
dist: snapshot

# Build a local release snapshot with goreleaser (no publish).
snapshot:
	goreleaser release --snapshot --clean
