# Changelog

All notable changes to tarjan are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Versioning starts fresh with the first public release from this repository;
development history from before the open-sourcing is not carried over.

## [Unreleased]

### Added

- Service-scoped tool checks: a `requires` tool may list the `services:` that
  need it. Untagged tools stay baseline (always checked); a tagged tool is
  checked only when one of its services is in the run's selection, so a partial
  `tarjan up <service>` skips toolchains it won't use. `tarjan doctor
  <service...>` scopes the same way, and the Starlark `tool()` builtin gains a
  `services` argument.
- Initial public release of tarjan: spin up a complete local development
  environment for a whole product from a single config file
  (`tarjan.yaml` / `tarjan.star`).
  - Core lifecycle: check required tools → clone repos → generate a VS Code
    workspace → start services in dependency order, gated on health checks →
    stream logs → clean teardown on `Ctrl+C`.
  - Services as local processes, Docker containers, remote (SSH) targets, or
    external endpoints; run-to-completion jobs; profiles and selective start.
  - Tool provisioning (opt-in): `mise` for versioned runtimes, host package
    managers for OS clients, bespoke `install` commands, and an agent (`--ai`)
    fallback.
  - Day-2 commands: `logs`, `status`, `ui`, `restart`, `reload`, `exec`,
    `pull`, `doctor`, `validate`, `workspace`, `down`, `upgrade`.
  - Cross-platform installers (`install.sh` / `install.ps1`) with mandatory
    checksum verification, plus in-place self-update via `tarjan upgrade`.
