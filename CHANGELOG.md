# Changelog

All notable changes to tarjan are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`setupCheck` — verified setup completion** — a service may declare a
  `setupCheck` command that must exit 0 for `setup` to count as done. tarjan
  runs it after the setup commands and only writes the one-shot completion marker
  when it passes; on later runs it re-checks a cached workspace and re-runs
  `setup` if the check now fails. This closes a trap where a setup command exits
  0 without producing what it should — e.g. an interrupted `npm install` leaves a
  package recorded but its postinstall-downloaded binary missing, so a plain
  re-run reports "up to date" and the workspace stays broken forever. With a
  `setupCheck` the broken state is caught (and self-healed) instead of frozen.
  Backwards compatible: services without a `setupCheck` behave exactly as before.
- **Per-repo tool versions** — a repo that pins its own versions (`mise.toml` /
  `.tool-versions` in its root) now gets them materialised automatically: before
  a service's setup/command runs, tarjan runs `mise install` in its workdir, so
  different repos can run different versions of the same runtime side by side
  (mise resolves per directory) and a repo's pin overrides the environment
  baseline from `requires:`. No-op for workdirs that pin nothing; a pinned
  workdir without mise installed warns and falls back to system tools.
- **Agent install fallback (`--ai`)** — for the long tail of tools no
  `install:`/`mise:`/`package:` provider covers, `tarjan up --install --ai` (or
  `tarjan doctor --install --ai`) shells out to an agent CLI (the Claude CLI by
  default) to install what the deterministic providers couldn't, then re-checks.
  tarjan never embeds an agent SDK — it drives whatever CLI is on `PATH`, so the
  default install path stays fast, offline, and dependency-free; the agent runs
  only with the explicit `--ai` opt-in (and only alongside `--install`). The CLI
  and its flags are overridable via `TARJAN_AI_CLI` / `TARJAN_AI_ARGS`.
- **General tool install providers** — a required tool can now declare *what* it
  needs instead of *how to install it per OS*, so `tarjan up --install` /
  `tarjan doctor --install` bring a machine up to spec in a way that scales to
  any future language or client:
  - `mise: <spec>` installs a **versioned language runtime** (dotnet, node,
    python, go, java, flutter…) through the [mise](https://mise.jdx.dev) version
    manager. The value is a mise spec (`dotnet@10`) or a bare name (`node`,
    versioned from `minVersion`). tarjan bootstraps mise itself when absent and
    puts its shims on `PATH`, so the started services use the managed version.
  - `package:` installs an **OS client tool** (psql, redis-cli…) through the
    host's auto-detected package manager (apt/brew/dnf/pacman/apk/zypper/choco/
    scoop/winget). It is one package name, or a map keyed by manager when the
    name differs (`{apt: postgresql-client, brew: libpq}`).
  The existing per-OS `install:` command stays as an escape hatch. When several
  are set on one tool the most explicit wins: `install` > `mise` > `package`.
  Installation remains strictly opt-in behind `--install`; without it a missing
  tool fails with the exact command it *would* run.

## [0.6.0] - 2026-07-08

### Added

- **Remote (SSH) targets** — a service can now run on another host with
  `remote: <name>` referencing a top-level `remotes:` map. A process service's
  `setup`/`command` run over `ssh` in the remote workspace (repos are cloned
  there automatically); a `docker` service runs on the remote's Docker daemon
  via `DOCKER_HOST=ssh://…` (pulled images and `build:` contexts alike). Each
  remote service's published/health ports are tunnelled back to the same
  `localhost` port (`ssh -L`), so local dependents, `localhost` health checks
  and browsers keep working unchanged; disable per-remote with `forward: false`.
  tarjan shells out to the system `ssh` (reusing your ssh config, agent, keys
  and `known_hosts`), so no credentials live in the config. `tarjan status`
  tags remote services with `@<remote>`, the Starlark schema gains a `remote()`
  builtin plus `remotes=`/`remote=`, and a repo's own `.tarjan` config may
  declare remotes too. See `examples/remote.yaml`. (`watch` live-restart is not
  yet supported for remote services.)
- **Community & security docs** — added `CONTRIBUTING.md` (dev setup, the local
  `make check` gate, and the Conventional Commits convention), `SECURITY.md`
  (private vulnerability reporting and the threat model), GitHub issue forms
  (bug / feature) plus a chooser config, and a pull-request template.

### Changed

- **Concurrency hardening** — the `Runner`'s `runCtx` and `startedAt` fields are
  now read and written under the runner mutex alongside the rest of its shared
  state. They were previously safe only by call ordering; guarding them makes
  the invariant explicit and refactor-proof (verified clean under `go test
  -race`).

### Tested / CI

- **Coverage gate** — `make cover` runs the suite with the race detector and
  fails if total statement coverage drops below a floor (`MIN_COVERAGE`, default
  66%). CI enforces it, so coverage can no longer silently regress. `make check`
  now includes the gate.
- **End-to-end smoke test** — a new `internal/e2e` test compiles the real
  binary and drives a full lifecycle (`up` → healthy via a command probe →
  `status` over the live control endpoint → `Ctrl+C` teardown → state cleared)
  against a self-contained config with no repos or external tools. It runs in
  CI and is skipped under `go test -short` (and on Windows). Companion failure
  scenarios assert the binary's exit contract: a required service that never
  gets healthy makes `up` exit non-zero, while a failing *optional* service is
  warned about and the environment still comes up.
- **More `cmd`/`workspace` coverage** — added tests for the `down`, `status`,
  `validate`, `workspace`, `doctor`, `reload`, `restart`, and `logs` commands
  and the `up` preflight helpers, and for `workspace` resolution / VS Code
  file generation. Added `runner` tests for the readiness-timeout failure path,
  the HTTP health probe, and the file-watch mtime scan. `cmd` rose from ~37% to
  ~55%, `workspace` to ~87%, and `runner` to ~70%; total coverage is now ~70%.
- **Filled test gaps** — added unit tests for previously untested packages
  (`state`, `shellx`, `gitutil`, `ui`) and broadened coverage of `config`
  (env-file layering, `Load`/in-place resolution), `runner` (docker argument
  builders, backoff, health probes), `selfupdate` (archive extraction and the
  GitHub release/asset HTTP paths via `httptest`), the `tui` dashboard model
  (`Update`/`View`, navigation, and the control-plane commands driven against a
  real loopback server), and the `cmd` layer (config resolution, exec/logs/init
  helpers, and the update-check throttle). Total coverage rose from ~49% to
  ~65%, with most packages now at 60–97%.

### Fixed

- **A reload racing shutdown no longer resurrects the state file** — a live
  `tarjan reload` runs its reconcile in the background. If `Ctrl+C` arrived
  while a reconcile was in flight, the reconcile's trailing `saveState` could
  rewrite the workspace state file *after* `Shutdown` had removed it, leaving a
  stale "environment running" record behind. `Shutdown` now sets a `stopping`
  flag before it tears down; an in-flight reconcile bails out, `saveState`
  becomes a no-op, and a reload requested after teardown is refused. (Covered
  by a deterministic ordering test and a concurrent reload/shutdown stress test
  run under `-race`.)

- **`up` no longer hangs on a service that starts but never gets healthy** — a
  required service whose process stayed alive while its health check kept
  failing would block `tarjan up` indefinitely: the readiness probe exhausted
  its own timeout, but the supervisor ignored that and never reported an
  outcome, so `up` waited forever for a service that would never be ready. The
  supervisor now treats a readiness timeout on a still-running process as a
  first-start failure, so a required service fails `up` fast at its health
  deadline and an optional one is warned about without sinking the environment.
  (Covered by new `runner` unit tests and end-to-end failure scenarios.)

- **`install.sh` works against the private repo** — the installer now
  authenticates with GitHub instead of assuming public, unauthenticated
  downloads (which 404 for a private repo). It uses the gh CLI when available
  (`gh auth login`), otherwise a token from `GH_TOKEN` / `GITHUB_TOKEN` over
  `curl`, and fetches assets through the API asset endpoint rather than the
  public download URL. With no credential it exits with clear guidance.

## [0.5.0] - 2026-07-04

### Added

- **`tarjan upgrade`** — update tarjan to the latest release in place: it checks
  GitHub, and if a newer version exists, downloads the matching binary, verifies
  its checksum, and atomically replaces the running executable. `--check` reports
  whether an update is available without installing it. Because the repo is
  private, requests authenticate with a token from `GH_TOKEN` / `GITHUB_TOKEN`,
  falling back to the gh CLI (`gh auth token`), and assets are fetched through
  the API asset endpoint.
- **Update notice on `up`** — `tarjan up` checks for a newer release in the
  background and prints a one-line notice once the environment is up. The check
  is throttled to once a day, never blocks or fails the run, and can be disabled
  with `TARJAN_NO_UPDATE_CHECK=1`.

## [0.4.0] - 2026-07-04

### Added

- **Already-running preflight** — `tarjan up` now refuses to start when an
  environment is already running for the same workspace, instead of letting
  its services collide on ports and surface as a cryptic
  `address already in use`. A responsive control server (or an orphaned
  service process left by a hard-killed run) is detected before any service
  starts, and the run stops with a clear message pointing at `tarjan down`. A
  state file left behind by a clean crash — nothing actually alive — is
  cleared automatically so it never blocks a legitimate restart.

## [0.3.0] - 2026-07-04

### Added

- **Orphaned container sweep** — every container tarjan runs now carries
  `tarjan.env` / `tarjan.service` labels, and each `tarjan up` force-removes
  containers labelled for this environment whose service is no longer in the
  config (removed or renamed). Same-named leftovers for services that still
  exist are handled per-service (see Fixed); this catches the orphans that
  name-matching cannot. The sweep matches against the full config, so a
  partial `up --only …` never removes another session's still-configured
  service.

### Fixed

- **Stale docker container name conflicts** — `tarjan up` now reclaims a
  container's name before starting it, removing any leftover container that
  holds it. Previously, a `tarjan up` that didn't exit cleanly (Ctrl+C that
  was interrupted, a crash, or a Docker daemon restart) could leave a
  `--rm` container behind; the next `up` then failed with
  `Conflict. The container name "/tarjan-<env>-<service>" is already in use`
  and the service never became healthy. Because the names are deterministic
  and owned by tarjan, the stale container is now force-removed before the
  fresh `docker run`.
- **`tarjan down` leaving exited containers** — `down` now force-removes each
  service's container after stopping it, so a container that had already
  exited without being auto-removed no longer lingers in `docker ps -a`.

## [0.2.0] - 2026-07-02

### Added

- **Per-repo config (`.tarjan/` directory)** — a repository can carry its own
  tarjan config at `.tarjan/tarjan.star|yaml|yml` (same schema as a top-level
  config), versioned alongside the code it runs. Two modes: (1) *in place* —
  inside such a checkout, `tarjan up` (and `validate`, `status`, `logs`,
  `exec`, `reload`, `down`, …) treats the checkout itself as the workspace, no
  clone or separate workspace dir; (2) *composition* — when a parent config
  clones a repo that carries a `.tarjan/` config, the repo's required tools and
  services are merged into the run, with `workdir`, `docker.build.context` and
  `envFile` paths rebased onto the repo's checkout. Repo services may
  `dependsOn` parent services (validated as a whole after merging); on a name
  collision the parent's definition wins, so the orchestrating config can
  override a repo's defaults.

- **Docker build-from-source** — a `docker` service can declare a `build`
  context (`context` / `dockerfile` / `args`) instead of a published `image`,
  and tarjan builds the image once per workspace before running it. This lets a
  config bring up a backend whose services are built from sibling repo
  checkouts (no registry images required). Available in YAML and Starlark
  (`docker(build = "repo", ...)`).
- **Docker command override** — a `docker` service can set `command` to override
  the image's default `CMD` (the args after the image in `docker run`), e.g. to
  run a server without its bundled migration step when migrations are handled by
  a separate job. Available in YAML and Starlark (`docker(command = [...])`).
- **Named, reusable workspaces** — set a top-level `version:` (or pass `tarjan up
  --version <label>`) to materialise the workspace at `<workspaceRoot>/<name>-<version>`
  instead of a timestamped directory. Repeated `up` runs of the same version
  reuse that directory (skipping clones and finished setup), and different
  versions keep parallel environments side by side. The previous timestamped
  behaviour is unchanged when no version is set.

## [0.1.0] - 2026-06-29

First release. tarjan brings up a product's entire local development environment
from a single config: it checks required tools, clones the repos, generates a
VS Code workspace, and supervises every service.

### Added

- **Core lifecycle** — `tarjan init`, `up`, `down`, `status`, `validate`. Each
  `up` materialises a fresh, timestamped workspace, clones repos into it, and
  generates a multi-root VS Code workspace.
- **Concurrent orchestration** — services start concurrently, each gated on its
  dependencies' health (TCP / HTTP / command probes); dependency order is
  topologically sorted with cycle detection.
- **Resilience** — crash isolation with per-service restart policies
  (`no` / `on-failure` / `always`) and exponential backoff; file-watch live
  restart (`watch`); captured logs persisted per service with `tarjan logs [-f]`.
- **Selective start** — `profiles` on services/repos plus `--only`, `--profile`,
  and `--no-deps` so one config serves backend-only, frontend, or full-stack.
- **Tool bootstrap** — `tarjan doctor` checks required tools and, with `--install`
  (opt-in), runs their per-OS install commands.
- **Hybrid local + cloud** — `external` dependencies (probed, not started),
  lifecycle `hooks` (preUp/postDown), and the tunnel-as-a-service pattern.
- **Env & secrets** — global and per-service `envFile` with layered precedence,
  keeping secrets out of git.
- **Jobs** — `kind: job` run-to-completion services; dependents wait for a
  successful exit (completion-gated), for migrations and ETL/ML pipelines.
- **Control plane** — a running `tarjan up` exposes a loopback, token-protected
  endpoint, enabling `tarjan restart <service>` (in-place restart), live
  `tarjan status`, `tarjan reload` (reconcile to an edited config), and `tarjan exec`.
- **Config in code** — optional Starlark `tarjan.star` (loops, conditionals,
  computed values) alongside YAML, run by a pure-Go interpreter.

[Unreleased]: https://github.com/stevenzg/tarjan/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/stevenzg/tarjan/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/stevenzg/tarjan/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/stevenzg/tarjan/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/stevenzg/tarjan/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/stevenzg/tarjan/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/stevenzg/tarjan/releases/tag/v0.1.0
