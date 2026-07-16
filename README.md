# tarjan

[![CI](https://github.com/stevenzg/tarjan/actions/workflows/ci.yml/badge.svg)](https://github.com/stevenzg/tarjan/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/stevenzg/tarjan?sort=semver)](https://github.com/stevenzg/tarjan/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/stevenzg/tarjan.svg)](https://pkg.go.dev/github.com/stevenzg/tarjan)
[![Go Report Card](https://goreportcard.com/badge/github.com/stevenzg/tarjan)](https://goreportcard.com/report/github.com/stevenzg/tarjan)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Conventional Commits](https://img.shields.io/badge/Conventional%20Commits-1.0.0-fe5196?logo=conventionalcommits&logoColor=white)](https://www.conventionalcommits.org)

**Spin up a complete local development environment for a whole product — from a single config file.**

Running a real product locally usually means a checklist no one enjoys: clone five repos, start Postgres, install the backend's dependencies, run the API, `npm install` the web app, start it, maybe boot a mobile app, and wire up the handful of cloud services you can't run locally. `tarjan` turns that checklist into one command.

```bash
tarjan up
```

It checks the tools you need, clones every repo into a fresh workspace, generates a VS Code workspace so all the repos open in one window, then starts every service in dependency order — each gated on a health check — and streams their logs. `Ctrl+C` tears the whole thing down cleanly.

Think **Terraform / Ansible / .NET Aspire, but for your *local* dev environment.**

> Status: early MVP. The core lifecycle (check → clone → workspace → orchestrate → teardown) works end-to-end. See [Roadmap](#roadmap).
>
> 📖 Landing page and full docs live in [`website/`](website/) (Next.js + Fumadocs, deployed to GitHub Pages).

---

## Why Go?

`tarjan` is written in Go on purpose — the whole point is that *running our tool* should require almost nothing:

| Goal | How Go delivers |
| --- | --- |
| **Minimal install** | Ships as a **single static binary**. No runtime, no `node_modules`, no .NET SDK just to launch the launcher. |
| **Mac + Windows + Linux** | One cross-compiled binary per platform; users never need a compiler. |
| **Fast startup** | Native binary, millisecond cold start — not a JVM/Node/.NET warm-up. |
| **Built for orchestration** | Goroutines make "start N services, watch their health, multiplex their logs" natural. |

The *product* you bring up can need .NET, npm, Docker, whatever — those get installed lazily, per environment. But the launcher itself stays dependency-free.

---

## Install

**macOS / Linux / WSL:**

```bash
curl -fsSL https://raw.githubusercontent.com/stevenzg/tarjan/main/install.sh | bash
```

**Windows PowerShell:**

```powershell
irm https://raw.githubusercontent.com/stevenzg/tarjan/main/install.ps1 | iex
```

The script detects your OS/arch, downloads the matching binary from the
[latest release](https://github.com/stevenzg/tarjan/releases/latest), verifies
its checksum, and puts `tarjan` on your `PATH`. Pin a version with
`VERSION=v0.1.0`. No GitHub credential is needed; a `GH_TOKEN` /
`GITHUB_TOKEN` is used when present to raise API rate limits (useful on CI).

Prefer not to pipe to a shell? Download a binary directly from
the releases page, or build it yourself:

```bash
# With Go 1.25+
go install github.com/stevenzg/tarjan@latest   # installs the `tarjan` binary

# or from a clone
git clone https://github.com/stevenzg/tarjan
cd tarjan
make build        # -> ./bin/tarjan (with version info)
```

Check your install with `tarjan version`, and update in place with
`tarjan upgrade` (add `--check` to only see whether a newer release exists).
`tarjan up` also prints a one-line notice when a newer release is available
(at most once a day; silence it with `TARJAN_NO_UPDATE_CHECK=1`).

## Quick start

```bash
tarjan init         # writes a starter tarjan.yaml (or `tarjan init --star` for Starlark)
$EDITOR tarjan.yaml # point it at your repos and services
tarjan up           # clone, check tools, generate workspace, start everything
```

Other commands:

```bash
tarjan upgrade                # update tarjan to the latest release (--check to only look)
tarjan doctor                 # check required tools are installed & up to date
tarjan doctor --install       # ...and install the missing ones (opt-in)
tarjan validate               # parse the config and print the service start order
tarjan pull                   # git pull every cloned repo in the current workspace
tarjan pull 0.1.0             # ...in the named "<name>-0.1.0" workspace
tarjan logs                   # list services with captured logs
tarjan logs api -f            # follow a service's log
tarjan status                 # live service status (ready/starting) of a running env
tarjan status --watch         # ...refreshed every second (lite dashboard)
tarjan ui                     # full-screen dashboard: logs + restart/reload keys
tarjan restart api            # restart one service in a running env, in place
tarjan reload                 # reconcile a running env to the edited config
tarjan exec api -- npm test   # run a command in a service's dir + environment
tarjan exec api               # ...or open a shell in its context
tarjan down                   # stop an environment started elsewhere
tarjan workspace --open       # (re)generate the VS Code workspace and open it
tarjan up --no-start          # just clone + prepare the workspace, don't run services
tarjan up --workspace ./dir   # reuse a specific workspace instead of a fresh one
tarjan up --version 0.1.0     # use a named, reusable workspace (<name>-<version>)
tarjan up api                 # start just this service (+ its dependencies)
tarjan up api web             # ...or several — positional args, same as --only
tarjan up --only api          # the flag form of the above
tarjan up --profile frontend  # activate a profile group (see Profiles below)
tarjan up --only web --no-deps  # exactly one service, skip dependency expansion
```

By default each `tarjan up` materialises a **fresh, timestamped workspace** under `workspaceRoot` (e.g. `~/tarjan/myproduct/20260628-150405/`), so every run starts from a clean slate and old runs stay around for reference.

Set a top-level `version:` (or pass `tarjan up --version <label>`) to instead use a **named, reusable workspace** at `<workspaceRoot>/<name>-<version>` (e.g. `~/tarjan/myproduct/myproduct-0.1.0/`). Repeated `up` runs of the same version reuse that directory — already-cloned repos and finished `setup` steps are skipped — so it comes back up fast. Use different versions (`--version pr-42`) to keep parallel environments side by side. `--workspace ./dir` still overrides with an explicit path.

---

## Configuration (`tarjan.yaml`)

```yaml
name: myproduct
workspaceRoot: ~/tarjan/myproduct        # each `up` creates a fresh dir under here

requires:                              # tools that must exist before starting
  - name: git
  - name: docker
    optional: true                     # warn instead of fail if missing
  - name: node
    minVersion: "20"
    mise: node@20                      # versioned runtime → mise (with --install)
  - name: psql
    package:                           # OS client → host package manager
      apt: postgresql-client
      brew: libpq

repos:                                 # cloned into the workspace
  - name: api
    url: https://github.com/your-org/api.git
    branch: main
  - name: web
    url: https://github.com/your-org/web.git
    dir: frontend/web                  # optional custom checkout path

services:                              # started in dependency order
  - name: postgres
    docker:                            # run as a container...
      image: postgres:16
      ports: ["5432:5432"]
      env: { POSTGRES_PASSWORD: dev }
    health:
      tcp: "localhost:5432"            # wait until the port accepts connections

  - name: api
    workdir: api                       # ...or as a local process in a repo
    setup: ["dotnet restore"]          # one-shot, runs once per workspace
    command: "dotnet run"
    env:
      ConnectionStrings__Default: "Host=localhost;Database=app;Username=postgres;Password=dev"
    dependsOn: [postgres]
    health:
      http: "http://localhost:8080/health"

  - name: web
    workdir: frontend/web
    setup: ["npm install"]
    command: "npm run dev"
    dependsOn: [api]

workspace:
  vscode: true                         # generate <name>.code-workspace
```

### Reference

**Tool** (`requires[]`)

| Field | Meaning |
| --- | --- |
| `name` | Executable looked up on `PATH`. |
| `minVersion` | Minimum acceptable version (compared as `major.minor.patch`). |
| `versionCommand` | How to probe the version (default `<name> --version`). |
| `mise` | Install a **versioned runtime** via the [mise](https://mise.jdx.dev) version manager. A mise spec (`dotnet@10`) or a bare name (`node`, versioned from `minVersion`). tarjan installs mise itself if absent and puts its shims on `PATH`. |
| `package` | Install an **OS client** via the host's auto-detected package manager (`apt`/`brew`/`dnf`/`pacman`/`apk`/`zypper`/`choco`/`scoop`/`winget`). One package name, or a map keyed by manager when it differs (`{apt: postgresql-client, brew: libpq}`). |
| `install` | Bespoke install command (escape hatch) — a single string, or a per-OS map (`darwin`/`linux`/`windows`). |
| `installHint` | Free-text pointer shown when the tool is missing and no provider above is set. |
| `optional` | Missing → warning instead of error. |

**Declare *what*, not *how-per-OS*.** `mise` handles versioned language runtimes
(dotnet, node, python, go, java, flutter…) and `package` handles OS client tools
(psql, redis-cli…), so the same config brings a machine up to spec on any OS —
and covers new languages/clients you add later without hand-writing per-OS
commands. When several are set on one tool, the most explicit wins:
**`install` > `mise` > `package`** (use `install` to override a provider for one
awkward tool).

**Per-repo versions.** A repo that pins its own tool versions — a `mise.toml` or
`.tool-versions` in its root — owns them: before a service's setup/command runs,
tarjan runs `mise install` in its workdir, so each repo gets exactly the runtime
it declares and different repos can use different versions of the same tool
side by side (mise resolves the version per directory). The `requires:` entries
are then just the environment baseline for repos that pin nothing. Keep the pin
in the repo, not in tarjan.yaml: the same file also drives CI, Docker builds and
IDEs, so a runtime upgrade is one PR in the repo that owns it.

Tool installation is **opt-in**: tarjan never installs anything without
`--install` (on `tarjan up` or `tarjan doctor`). Without it, a missing tool fails
with the exact command it *would* run, so nothing happens to your machine
behind your back. A new contributor gets a working toolchain with:

```bash
tarjan doctor --install   # install whatever's missing, then you're ready
tarjan up
```

**Agent fallback (`--ai`).** For the long tail of tools no provider covers, add
`--ai` to `--install`: tarjan shells out to an agent CLI (the [Claude
CLI](https://claude.com/claude-code) by default) to install what the
deterministic providers couldn't, then re-checks. tarjan never embeds an agent —
it just drives whatever CLI is on `PATH`, so the default path stays fast,
offline, and dependency-free; the agent runs only when you ask for it.

```bash
tarjan doctor --install --ai   # deterministic first, agent for the rest
```

Override the CLI or its flags with `TARJAN_AI_CLI` / `TARJAN_AI_ARGS` (the prompt
is fed on stdin). Note the Claude CLI's `bypassPermissions` mode is refused when
running as root, so the fallback targets a normal (non-root) developer machine.

**Repo** (`repos[]`): `name`, `url`, `branch` (optional), `dir` (optional checkout path).

**Service** (`services[]`)

| Field | Meaning |
| --- | --- |
| `name` | Unique service name. |
| `kind` | `service` (default, long-running) or `job` (run-to-completion; see below). |
| `workdir` | Working directory inside the workspace (usually a cloned repo). |
| `setup` | One-shot commands run once per workspace before first start. |
| `command` | Long-running command (ignored when `docker` is set). |
| `docker` | Run as a container: `image` (pull) **or** `build` (build from source), plus `ports`, `env`, `volumes`, `args`, and `command` (override the image's CMD). |
| `external` | A cloud/remote dependency tarjan does *not* start — only health-probes for reachability (see below). |
| `env` | Extra environment variables for setup + command. |
| `envFile` | `.env` files (config-dir-relative or absolute) loaded for this service. |
| `dependsOn` | Services that must be **healthy** first. |
| `health` | Readiness probe: `tcp`, `http`, or `command` (+ `timeout`, `interval`). |
| `optional` | Failure → warning instead of aborting the whole `up`. |
| `restart` | Crash policy: `no` (default), `on-failure`, or `always`. |
| `maxRestarts` | Cap on automatic restarts (default 5; `0` = unlimited). |
| `watch` | Live-restart on file change: `paths` (relative to `workdir`) + `debounce`. |

`tarjan` topologically sorts services by `dependsOn` (and rejects cycles). Independent services start **concurrently**; each waits only for *its own* dependencies to become healthy, so a five-service environment doesn't pay for a strict serial boot.

### Profiles & selective start

One config can serve every shape of work. A service or repo with **no** `profiles`
is always included; one **with** profiles is included only when a matching
profile is active (`--profile`), it's named as a positional argument / `--only`,
or it's pulled in as a dependency.

Name services **positionally** to start just those — `tarjan up web` is the same
as `tarjan up --only web`, and the two combine. Either way a service's
`dependsOn` are pulled in too (disable with `--no-deps`), so you get the service
*and everything it needs* from one word.

```yaml
services:
  - { name: api,  command: "...", profiles: [] }            # always on (core)
  - { name: web,  command: "...", dependsOn: [api], profiles: [frontend, full] }
  - { name: app,  command: "...", dependsOn: [api], profiles: [mobile, full] }
```

```bash
tarjan up                     # api only (the always-on core)
tarjan up web                 # web + its dependency api  (positional selection)
tarjan up web app             # web + app + api
tarjan up --profile frontend  # api + web
tarjan up --profile full      # api + web + app
tarjan up --only web          # the flag form of `tarjan up web`
tarjan validate web              # preview a selection without running it
tarjan validate --profile full   # ...or preview a whole profile
```

Profiles also gate `repos`, so a frontend-only run won't clone the backend or
mobile repositories.

### Jobs (run-to-completion)

Not everything is a long-running server. A `kind: job` runs once and its
dependents wait for it to **exit 0** — completion-gated, not health-gated. This
is the shape for database migrations, seed data, and ETL/ML pipeline steps.

```yaml
services:
  - { name: db, docker: { image: postgres:16, ports: ["5432:5432"] }, health: { tcp: "localhost:5432" } }
  - name: migrate
    kind: job
    command: "alembic upgrade head"
    dependsOn: [db]            # runs after db is healthy
  - name: api
    command: "uvicorn app:api"
    dependsOn: [migrate]       # starts only after migrate exits 0
```

A failed job (non-zero exit) fails the run instead of starting its dependents.
Chain jobs with `dependsOn` to express a pipeline (extract → transform → load).

### Building docker images from source

A `docker` service usually pulls a published `image`. When the service is one of
*your* repos — it ships a `Dockerfile`, not a registry image — give it a `build`
context instead and tarjan builds the image (the equivalent of `docker compose
build`) before running it:

```yaml
services:
  - name: api
    docker:
      build:
        context: orame-service          # a cloned repo (relative to the workspace)
        dockerfile: Dockerfile          # optional (relative to context)
        args: { TARGET: dev }           # optional --build-arg values
      ports: ["8080:8080"]
      env: { ASPNETCORE_ENVIRONMENT: Development }
    health: { http: "http://localhost:8080/health/ready" }
```

The image is built **once per workspace** (alongside `setup`), so a fresh `tarjan
up` rebuilds — picking up source changes — while in-workspace restarts reuse it.
The derived tag is `tarjan-<config>-<service>:dev`; set `image:` alongside `build:`
to name the tag yourself. This lets tarjan bring up a whole backend whose services
are built from sibling checkouts, with no published images required.

When an image bundles more than one entrypoint (say it runs a migration step
*then* the server), override its default `CMD` with `command` to run just the
part you want — the rest of the stack handles migrations separately:

```yaml
  - name: agents
    docker:
      build: { context: orame-agents }
      ports: ["8001:8000"]
      command: ["uvicorn", "orame_agents.main:app", "--host", "0.0.0.0", "--port", "8000"]
```

### Env files & secrets

Keep secrets out of both git and the config with `.env` files. A global
`envFile` applies to every service; a per-service `envFile` applies to one.
Precedence, lowest to highest:

```
process env  <  global envFile  <  service envFile  <  inline env:
```

```yaml
envFile: [.env.shared]          # top-level: loaded for every service
services:
  - name: api
    envFile: [.env.api]         # service-specific
    env: { PORT: "8080" }       # inline wins over files
```

Paths are resolved relative to the config directory (or absolute). Commit a
`.env.example` and git-ignore the real `.env*`; a missing referenced file is a
clear error, not a silent skip.

### Local + cloud (hybrid) environments

Real products often need things that don't run locally — a managed database, a
SaaS API, a service that only lives in a dev cluster. tarjan models these without
trying to run them:

- **External dependencies** (`external: true`): not started, only health-probed
  for reachability. Local services `dependsOn` them, so if the cloud DB is down
  you find out *before* everything else boots — and `tarjan status` shows them as
  `external`.
- **Tunnels are just services.** A `kubectl port-forward` or `ssh -L` is a
  long-running command that holds a port open — model it as a normal service
  with `restart: always` and a `health.tcp` on the local port; dependents wait
  for the tunnel to be up.
- **Lifecycle hooks** run global one-shot commands around the environment:

  ```yaml
  hooks:
    preUp:   ["gcloud auth login", "fetch-secrets > .env.cloud"]  # before services
    postDown: ["echo done"]                                        # after stop
  ```

See [`examples/hybrid-cloud.yaml`](examples/hybrid-cloud.yaml) for all three together.

### Remote targets (SSH)

Some pieces of a product shouldn't — or can't — run on your laptop: a GPU
trainer, a heavy build, a service you keep on a shared dev box. Declare a
**remote** and point a service at it with `remote:`; everything else — the
dependency graph, health gating, restart policies, captured logs, `reload` —
works exactly as it does locally.

```yaml
remotes:
  devbox:
    host: dev.example.com        # or an alias from your ~/.ssh/config
    user: steven                 # optional
    identityFile: ~/.ssh/id_ed25519   # optional
    # workspaceRoot: tarjan/shop # where repos clone on the remote (default)
    # forward: true              # tunnel ports back to localhost (default)

services:
  - name: trainer                # (A) a process, run over ssh on the remote
    remote: devbox
    workdir: trainer             # resolves under the remote's workspaceRoot
    setup: ["pip install -r requirements.txt"]
    command: "python serve.py"
    health: { http: "http://localhost:9000/health" }

  - name: db                     # (B) a container, on the remote Docker daemon
    remote: devbox
    docker: { image: postgres:16, ports: ["5432:5432"] }
    health: { tcp: "localhost:5432" }
```

Two execution modes, chosen automatically:

- **(A) A process runs over `ssh`.** Its `setup` and `command` run on the
  remote host, in `workspaceRoot/<workdir>`, with the service's `env`/`envFile`
  exported into the remote shell. tarjan clones the repos those services need
  onto the remote for you.
- **(B) A `docker` service runs on the remote's Docker daemon** (via
  `DOCKER_HOST=ssh://…`). `image` is pulled there; a `build:` context ships from
  your machine to the remote daemon — no published image required.

**Ports keep working at `localhost`.** For each remote service, tarjan opens an
SSH tunnel (`ssh -L`) that forwards its published/health ports back to the same
`localhost` port. So local dependents' `dependsOn`, `localhost` health checks,
and your browser all keep hitting `localhost` — no config changes. Disable
per-remote with `forward: false`.

tarjan shells out to your system `ssh` (like it does for `git`/`docker`), so
your ssh config, agent, keys and `known_hosts` all apply — no keys live in
`tarjan.yaml`. Requirements & limits: the remote needs `git`/`docker` for what
it runs; auth should be non-interactive (key/agent); `watch` live-restart isn't
supported for remote services; and clean remote teardown relies on the SSH
session ending, so remote commands should honour SIGTERM/SIGHUP. `tarjan status`
tags each remote service with `@<remote>`. See
[`examples/remote.yaml`](examples/remote.yaml).

### Runtime behaviour

- **Crash isolation + restart.** A service that exits is handled per its `restart`
  policy (with exponential backoff) — it does *not* tear the rest of the
  environment down. `on-failure` restarts only on a non-zero exit; `always`
  restarts on any exit.
- **Live restart on change.** With `watch`, edits under the given paths restart
  just that service (these restarts don't count against `maxRestarts`).
- **Captured logs.** Every service's output is streamed to the terminal
  (prefixed, colored) *and* persisted to `<workspace>/.tarjan/logs/<service>.log`,
  readable later with `tarjan logs <service>` (`-f` to follow).
- **Control from another terminal.** A running `tarjan up` exposes a loopback,
  token-protected control endpoint (recorded in `.tarjan/control.json`), so
  `tarjan restart <service>` restarts one service in place and `tarjan status`
  shows live readiness — without interrupting the rest of the environment.
- **Live reload.** Edit `tarjan.yaml`, run `tarjan reload`, and the running
  environment reconciles to it: **added** services start, **removed** ones stop,
  and **changed** ones restart with their new spec — the rest keep running. The
  new config is validated before anything changes, and a changed service is
  fully stopped before its replacement starts (so it never collides with its own
  port). Unchanged services aren't touched.

See [`examples/`](examples/) for microservices, full-stack, and backend+frontend+app setups.

### Config in code (Starlark)

YAML is the default and right for most setups. When a config has **logic** —
many near-identical services, per-environment conditionals, computed ports/URLs
— a static file means copy-paste and drift. For those, write `tarjan.star` in
[Starlark](https://github.com/google/starlark-go) (a small, sandboxed
Python-like language) instead. It produces the *same* config, so every feature
above works unchanged; the interpreter is pure Go, so tarjan stays a single binary
with no extra toolchain for you to install.

```python
# tarjan.star — N near-identical services become a loop, not N copy-pasted blocks
services = [service(name = "db", docker = docker(image = "postgres:16", ports = ["5432:5432"]),
                    health = health(tcp = "localhost:5432"))]
for i, name in enumerate(["catalog", "orders", "gateway"]):
    services.append(service(
        name = name, workdir = name, command = "go run ./cmd/server",
        env = {"PORT": str(8080 + i)}, depends_on = ["db"], restart = "on-failure",
    ))

tarjan = config(name = "shop", services = services, workspace = workspace(vscode = True))
```

tarjan auto-detects `tarjan.star` (preferred) or `tarjan.yaml`. The builtins mirror the
YAML schema: `config`, `repo`, `tool`, `service`, `docker`, `health`, `watch`,
`hooks`, `workspace` (kwargs are snake_case, e.g. `depends_on`, `min_version`).
The script must assign its result to a top-level `tarjan`. `tarjan reload` works with
Starlark too. See [`examples/microservices.star`](examples/microservices.star).

### Per-repo config (`.tarjan/` directory)

A repository can carry its **own** tarjan config in a `.tarjan/` directory at its
root — `.tarjan/tarjan.star`, `.tarjan/tarjan.yaml` or `.tarjan/tarjan.yml`, the
same schema as a top-level config. It describes how to run *that* repo (its
required tools, databases, jobs, services), versioned alongside the code that
needs it. It works in two ways:

**Run a repo in place.** Inside a checkout that has a `.tarjan/` config, just:

```bash
cd myrepo
tarjan up          # also: validate, status, logs, exec, reload, down …
```

No separate workspace is materialised — the checkout *is* the workspace.
Services run against your working tree (an empty `workdir` means the repo
root), and runtime files (logs, state) live under the same `.tarjan/`
directory; add them to the repo's `.gitignore`:

```gitignore
.tarjan/logs/
.tarjan/state.json
.tarjan/control.json
.tarjan/setup-*
```

**Compose repos that bring their own config.** When a top-level config clones a
repo that carries a `.tarjan/` config, the repo's required tools and services
are merged into the run automatically — the orchestrating config only has to
say *which* repos make up the product, not how to run each one:

```yaml
# tarjan.yaml — the product config stays a repo list
name: shop
repos:
  - name: api        # has .tarjan/tarjan.yaml → contributes postgres, migrate, server…
    url: https://github.com/your-org/api.git
  - name: web        # has .tarjan/tarjan.yaml → contributes its dev server
    url: https://github.com/your-org/web.git
```

Merged services are rebased onto the repo's checkout: a relative `workdir`
(or none) resolves inside the repo, as do `docker.build.context` and `envFile`
paths. A repo service may `dependsOn` services defined by the parent config or
by other repos; the combined config is validated as a whole after merging. If
the parent config defines a service with the same name, **the parent wins** —
so the orchestrating config can override a repo's defaults (e.g. share one
postgres). Tools required by repo configs are checked (and with `--install`,
installed) like the parent's own. `tarjan reload` re-reads repo configs too.

---

## How it works

```
tarjan up
  ├─ check required tools (presence + minVersion)
  ├─ create fresh workspace:  <workspaceRoot>/<timestamp>/
  ├─ git clone every repo into it
  ├─ write <name>.code-workspace  (all repos, one IDE window)
  └─ launch every service concurrently; each one:
        waits for its dependencies to be healthy
        → run setup (once) → start (process or docker) → probe health
        → supervise: restart on crash (policy) or on file change (watch)
     stream + persist logs · Ctrl+C → reverse-order graceful shutdown
```

Local processes run in their own process group so shutdown reliably stops the
whole tree (shell + children); docker services are stopped via `docker stop`.

## Development

```bash
make build      # -> ./bin/tarjan
make test       # go test ./...
make lint       # golangci-lint
make check      # fmt-check + vet + lint + test (what CI runs)
make hooks      # install the local pre-commit hook (one-time)
```

Quality is enforced at two levels:

- **Local git hooks** (`.githooks/`, enabled via `make hooks`): a fast,
  zero-extra-dependency `pre-commit` gate — `gofmt`, `go vet`, `go test`, and
  `golangci-lint` if it's installed — plus a `commit-msg` hook enforcing
  [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, …). Bypass deliberately with
  `git commit --no-verify`.
- **CI** (`.github/workflows/ci.yml`): the authoritative gate on every push and
  PR — `gofmt` check, `go vet`, `golangci-lint`, `go test -race`, and a
  cross-compile matrix (macOS/Linux/Windows). Local hooks can be skipped; CI
  can't.

Linters are configured in [`.golangci.yml`](.golangci.yml).

## Roadmap

Done: concurrent dependency-ordered start · crash isolation + restart policies ·
file-watch live restart · captured logs + `tarjan logs` · profiles & selective
start (positional `tarjan up <service>` / `--only` / `--profile`) · update cloned
repos in place (`tarjan pull`) · opt-in tool install (`tarjan doctor --install`) ·
hybrid local+cloud (external dependencies, tunnels, lifecycle hooks) · env files
& secrets out of git · run-to-completion jobs · `tarjan restart` + live status ·
live config reload (`tarjan reload`) · config in code (Starlark `tarjan.star`) ·
full-screen dashboard (`tarjan ui`) · run a command in a service's context
(`tarjan exec`) · remote (SSH) targets — process over ssh and docker on a remote
daemon, with auto port-forwarding.

This roadmap is complete. Possible future work: non-SSH remote targets
(Kubernetes / other cluster backends), `watch` live-restart for remote services.

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for the dev
setup, the local gate (`make check`), and the commit-message convention. For
security issues, please follow [SECURITY.md](SECURITY.md) rather than opening a
public issue.

## License

MIT — see [LICENSE](LICENSE).