# Security Policy

## Supported versions

`tarjan` is pre-1.0 and ships from `main`. Security fixes are applied to the
latest release and the `main` branch. Older tagged releases do not receive
backports — please upgrade to the newest version (`tarjan upgrade`).

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
pull requests, or discussions.**

Instead, report them privately using GitHub's built-in
[private vulnerability reporting](https://github.com/stevenzg/tarjan/security/advisories/new)
("Report a vulnerability" under the repository's **Security** tab). This creates
a private advisory only the maintainers can see.

Please include:

- A description of the issue and its impact.
- Steps to reproduce (a minimal `tarjan.yaml` / `tarjan.star` and command line
  where relevant).
- The `tarjan version` output and your OS/arch.
- Any suggested remediation, if you have one.

You can expect an initial acknowledgement within a few days. We will keep you
informed of progress toward a fix and coordinate disclosure timing with you.

## Scope and threat model

`tarjan` orchestrates local development environments. Keep the following in mind
when assessing impact:

- **It runs code you point it at.** `tarjan` clones repositories and runs the
  `setup`/`run` commands defined in a project's `tarjan.yaml` or `tarjan.star`.
  A malicious config or repository can run arbitrary commands on your machine —
  the same trust level as running `make` or `npm install` in that project. Only
  run configs you trust.
- **Secrets and env files.** `tarjan` loads env files that are intentionally
  kept out of git. Reports about secrets being logged, written to
  world-readable locations, or leaked into child processes unintentionally are
  in scope.
- **The installer and self-update path.** `install.sh` / `install.ps1` and
  `tarjan upgrade` download release binaries. Issues that would let an attacker
  substitute or tamper with a downloaded binary are in scope.
- **Out of scope:** vulnerabilities in the third-party services, tools, or
  repositories a user chooses to orchestrate; social-engineering a user into
  running an untrusted config.
