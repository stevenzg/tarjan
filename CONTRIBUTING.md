# Contributing to tarjan

Thanks for taking the time to contribute! This guide covers everything you need
to get a change merged.

## Development setup

You need **Go 1.25+** and `git`. That's it — the launcher itself is
dependency-free.

```bash
git clone https://github.com/stevenzg/tarjan.git
cd tarjan
make build        # builds ./bin/tarjan
make hooks        # install the local pre-commit + commit-msg hooks (recommended)
```

Optional tooling for the full local gate:

- [`golangci-lint`](https://golangci-lint.run) v2.5+ — matches the CI linter.
- [`goreleaser`](https://goreleaser.com) — only needed for `make snapshot`.

## The local gate

Every pull request must pass the same checks CI runs. You can run them all
locally in one shot:

```bash
make check        # fmt-check + vet + lint + cover (race + coverage floor)
```

Individual targets:

| Command | What it does |
| --- | --- |
| `make fmt` | Format the tree with `gofmt`. |
| `make fmt-check` | Fail if anything is not gofmt-ed (CI gate). |
| `make vet` | `go vet ./...`. |
| `make lint` | `golangci-lint run ./...`. |
| `make test` | Run the suite (no race, no coverage gate). |
| `make cover` | Run with `-race` and enforce the `MIN_COVERAGE` floor. |

If you installed the hooks with `make hooks`, the fast checks run automatically
on every commit. Bypass them for a work-in-progress commit with
`git commit --no-verify`.

## Tests

- Keep changes covered. New behaviour should ship with tests; the coverage gate
  (`make cover`) will fail the build if total statement coverage drops below the
  floor set in the `Makefile` (`MIN_COVERAGE`).
- Concurrency-sensitive code (anything under `internal/runner`) must stay clean
  under the race detector — `make cover` runs `go test -race`.
- Prefer table-driven tests and avoid reaching for real network, real Docker, or
  the real filesystem outside of `t.TempDir()`.

## Commit messages

This repo enforces [Conventional Commits](https://www.conventionalcommits.org)
via the `commit-msg` hook (and reviewers). The subject must look like:

```
<type>[optional scope][!]: <description>
```

Allowed types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`,
`build`, `ci`, `chore`, `revert`. Keep the subject at or under 100 characters.

Examples:

```
feat(runner): add live reload
fix: stop external service on shutdown
docs: document the --profile flag
```

## Pull requests

1. Branch off `main`.
2. Make your change, with tests, keeping `make check` green.
3. Update `CHANGELOG.md` under the `## [Unreleased]` heading when your change is
   user-visible.
4. Open the PR and fill in the template. Cross-compilation for every shipped
   platform is verified by CI (`darwin/arm64`, `darwin/amd64`, `linux/amd64`,
   `linux/arm64`, `windows/amd64`) — if you touch OS-specific code
   (`*_unix.go` / `*_windows.go`), build the matrix locally too.

Keep PRs focused: one logical change per PR makes review faster.

## Releasing (maintainers)

Releases are cut from the **Actions** tab — no hand-written tag or version:

1. Run the **Release (manual)** workflow (`.github/workflows/release-dispatch.yml`)
   and pick a bump — `patch` / `minor` / `major` — or type an explicit `vX.Y.Z`.
2. It computes the next version from the latest tag, tags the current `main`, and
   runs [goreleaser](https://goreleaser.com) to build the cross-platform binaries
   and publish the GitHub Release. Release notes are generated from the
   Conventional Commit messages, so keep commit subjects meaningful.

Pushing a `vX.Y.Z` tag by hand still works too (it triggers
`.github/workflows/release.yml`) — handy for re-running a release.

## Reporting bugs and requesting features

Use the [issue templates](https://github.com/stevenzg/tarjan/issues/new/choose).
For anything security-related, **do not** open a public issue — see
[SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
