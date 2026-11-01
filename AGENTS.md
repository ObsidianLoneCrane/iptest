# Project Development Rules

This file applies to the entire repository. Read [README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md) before starting development, and follow their project scope and branch workflow for subsequent changes.

## Branch Workflow

- Check the current branch, working-tree changes, and relevant remote state before starting a task.
- Use `main`, `develop`, and `feature/*` branches. Make feature, fix, refactoring, documentation, test, and configuration changes on `feature/*` branches.
- Create task branches from the latest `develop`. An existing, unmerged feature branch may be reused if it is suitable for the current task.
- Do not perform routine development directly on `main` or `develop`; syncing and merging branches through the established workflow are exceptions.
- Use squash merge by default for `feature/* -> develop`. Pull requests must explicitly target `develop`.
- Use a regular merge for `develop -> main` to preserve history. After a release, merge `main -> develop` using a regular merge; allow a fast-forward when possible.
- Do not reuse a squash-merged feature branch for a new task, rewrite shared branch history, or automatically force-delete branches.
- Preserve existing user changes. Do not automatically discard, overwrite, or hide unrelated changes.
- Act within the scope already authorized in the current conversation. This file defines the workflow; it does not mean that every commit, push, or release has been requested.

## Project Scope

- This project is a Go CLI for personal use, targeting Windows, Ubuntu, Arch Linux, CachyOS, and macOS. Android support through Termux is reserved for future evaluation.
- Normal output contains only local interface IP addresses, public egress IP addresses, country/state or province/city, and available third-party IP information. Format source fields as a Field/Status/Meaning table under a protocol-family and source heading. Display source fields without an overall cleanliness verdict, risk grade, or local sharing threshold; leave interpretation to the user.
- Prioritize the selected proxy. Do not silently fall back to a direct connection after a proxy failure. Once an egress IP is obtained, explicitly query that same IP with subsequent data sources.
- Omit missing, invalid, empty, or unusable source fields. Preserve explicit boolean false values as source facts; a zero device estimate is not proof of zero users and is omitted. Missing optional source data alone is not an inspection failure. Report actual lookup failures through safe diagnostics, and never interpret missing data as a clean reputation.
- Do not present unfinished features or planned commands as implemented, or cross-compilation results as successful runtime verification.
- Keep the implementation small, favor the standard library and pure Go dependencies, and organize code according to actual needs.

## Project-local Go Environment

- Use the temporary development environment provided by `scripts/setup-go.ps1`, `scripts/go.ps1`, and `scripts/clean-go.ps1`. Read the development environment sections in README and CONTRIBUTING before invoking Go.
- The current scripts support Windows PowerShell 5.1 and PowerShell 7 on Windows. Run `.\scripts\setup-go.ps1` from the repository root when the pinned SDK is missing; the initial setup requires an official SDK download and SHA-256 verification.
- The SDK is pinned to `1.27.3` in `.go-version`. The `go 1.26.0` directive in `go.mod` is the module's minimum version, not the SDK pin. Keep these roles distinct.
- Invoke Go through `.\scripts\go.ps1`, including environment inspection, formatting, builds, vet, and tests. Do not use or install a global Go toolchain for this project, persistently alter `PATH` or `HOME`, or use `go env -w`.
- Keep SDK downloads and extracted tools under `.tools/go`. Keep `GOPATH`, build and module caches, and Go temporary files under `.cache/go`; both directories are ignored by Git.
- The launcher temporarily configures its process environment with `GOROOT`, `GOPATH`, `GOCACHE`, `GOMODCACHE`, `GOTMPDIR`, `APPDATA`, `LOCALAPPDATA`, `TEMP`, and `TMP`, with `GOENV=off` and `GOTOOLCHAIN=local`. The Go child process inherits these values; `finally` restores the previous process environment. Do not replace this with persistent user-level or system-level configuration.
- Disable telemetry through the official `go telemetry off` command with project-local configuration storage. `GOTELEMETRY` is a read-only status query, not an environment variable to set for disabling telemetry.
- Use `.\scripts\clean-go.ps1 -WhatIf` to preview cleanup. The cleanup script removes only `.tools/go` and `.cache/go`, retaining source files and application outputs in `bin`.
- Go is a development dependency. Users of the compiled application do not need Go installed.
- Verify scripts on each target host before claiming support. The current environment scripts support Windows only; Linux/macOS setup is future work and needs separate implementation and verification. Do not infer Android support from the Go environment setup.

## Changes and Validation

- Use clear commit prefixes such as `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, and `chore:`. Keep each change focused on one task.
- Use UTF-8 for documentation and follow `.gitattributes`. Check documentation content and diffs when making documentation changes.
- Once source packages exist, run `gofmt` through `.\scripts\go.ps1 fmt ./...` and perform applicable `.\scripts\go.ps1 vet ./...`, `.\scripts\go.ps1 test ./...`, and platform checks through the same launcher.
- Do not introduce unrelated tests or create a Go module early for documentation-only changes.
- Keep credentials and local configuration out of version control, and redact sensitive information from logs.
- When finishing a task, report the current branch, changes made, validation actually performed, and whether commits, pushes, or merges have been completed.
