# Project Development Rules

This file applies to the entire repository. Read [README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md) before starting development, and follow their project scope and branch workflow for subsequent changes.

## Branch Workflow

- Check the current branch, working-tree changes, and relevant remote state before starting a task.
- Keep `main`, `develop`, and the single `release` branch long-lived. Use short-lived `feature/*` branches for feature, fix, refactoring, documentation, test, and configuration changes. Do not create version-specific `release/*` branches or delete a long-lived branch after merging a PR.
- Create routine task branches from the latest `develop`. An existing, unmerged feature branch may be reused if suitable. A fix specifically for a frozen release candidate branches from `release` and targets `release`; synchronize the fix back to `develop` through a regular merge.
- Do not perform routine development directly on `main`, `develop`, or `release`; reviewed promotions and synchronization merges are exceptions.
- Use squash merge by default for `feature/* -> develop` and candidate-fix `feature/* -> release`. Confirm the intended PR target explicitly.
- Promote a selected candidate using `develop -> release`, then publish approved candidates using `release -> main`. Use regular merges between all long-lived branches; never squash/rebase those promotions. After publication, synchronize `main -> release` and `main -> develop` using regular merges, allowing fast-forwards when possible.
- Freeze one candidate at a time on `release`. Do not automatically pull newer `develop` features into an active candidate; any candidate change requires the affected validation to run again. Record the candidate commit and the final tag/artifact checksums.
- Do not reuse a squash-merged feature branch for a new task, rewrite shared branch history, or automatically force-delete branches.
- Preserve existing user changes. Do not automatically discard, overwrite, or hide unrelated changes.
- Act within the scope already authorized in the current conversation. This file defines the workflow; it does not mean that every commit, push, or release has been requested.

## Project Scope

- This project is a cross-platform Go CLI targeting Windows, Ubuntu, Arch Linux, CachyOS, and macOS, intended to work with different users' network configurations. Android support through Termux is reserved for future evaluation.
- Normal output includes the selected network path, local interface addresses and sampled traffic rates, public egress addresses, country/state or province/city, available third-party IP information, and bounded HTTP download/latency measurements. Format source fields as a native bordered Field/Status/Meaning table with wrapping. Display source fields without an overall cleanliness verdict, risk grade, or local sharing threshold; leave interpretation to the user.
- Default to a grouped `report` with native tables, wrapped cells, per-interface rates shown once, and completed sections retained on cancellation. Keep report tables separate from stderr progress. Preserve optional `--format plain` output and omit empty source sections; formatting must not change inspection policy or exit status.
- Automatic discovery must honor enabled system settings and distinguish unsupported PAC configurations from an absent application proxy. A system route may include TUN/VPN; never label it as proven direct. Do not scan arbitrary local ports or modify operating-system proxy settings.
- Sample interface traffic before sending probe traffic; report per-interface rates without summing virtual and physical counters. Active measurements reuse the selected HTTP route, bound transfer bytes/time, identify the target, and do not claim maximum ISP bandwidth or that all destinations share one egress.
- Default to IPv4-only local-address output and IP inspection; IPv6 is opt-in through `--family`. Speed measurements use sequential public-target failover by default, with optional serial comparison. Enforce shared time/body-byte budgets including failed attempts, preserve completed latency on download failure, and never switch the selected proxy policy when switching targets.
- Prioritize the selected proxy. Do not silently fall back to a direct connection after a proxy failure. Once an egress IP is obtained, explicitly query that same IP with subsequent data sources.
- Omit missing, invalid, empty, or unusable source fields. Preserve explicit boolean false values as source facts; a zero device estimate is not proof of zero users and is omitted. Missing optional source data alone is not an inspection failure. Report actual lookup failures through safe diagnostics, and never interpret missing data as a clean reputation.
- Do not present unfinished features or planned commands as implemented, or cross-compilation results as successful runtime verification.
- A formal release must provide ready-to-run Windows, Linux, and macOS artifacts and native runtime evidence for every advertised OS/CPU target. Validate extracted final packages without a Go SDK or development environment; record platform versions and required OS utilities. Include desktop proxy/counter acceptance for advertised Linux distributions and macOS, plus downloaded-package first launch and executable permissions. Public-service availability is separate from application/platform correctness. See `docs/releasing.md`; configured CI is not evidence that its jobs have actually passed.
- Use the MIT license in `LICENSE`, include it in release packages, and preserve its copyright/license notice. Package Windows as ZIP and Linux/macOS as tar.gz with executable mode preserved. Candidate builds and GitHub Actions artifacts are not automatically published Releases.
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
- On disposable GitHub-hosted runners only, the native validation workflow may use official `actions/setup-go` with `.go-version` and invoke that provisioned SDK directly. Runner-managed SDK/telemetry configuration is ephemeral; build/module/temp state stays under workspace `.cache/go`. Disable telemetry with `go telemetry off`, keep `GOENV=off`, `GOTOOLCHAIN=local`, and `CGO_ENABLED=0`, and do not persist user configuration. This CI-only allowance does not replace the project-local launcher requirement on the development host.
- Verify scripts on each target host before claiming support. The current environment scripts support Windows only; Linux/macOS setup is future work and needs separate implementation and verification. Do not infer Android support from the Go environment setup.

## Changes and Validation

- Use clear commit prefixes such as `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, and `chore:`. Keep each change focused on one task.
- Use UTF-8 for documentation and follow `.gitattributes`. Check documentation content and diffs when making documentation changes.
- Once source packages exist, run `gofmt` through `.\scripts\go.ps1 fmt ./...` and perform applicable `.\scripts\go.ps1 vet ./...`, `.\scripts\go.ps1 test ./...`, and platform checks through the same launcher.
- Do not introduce unrelated tests or create a Go module early for documentation-only changes.
- Keep credentials and local configuration out of version control, and redact sensitive information from logs.
- When finishing a task, report the current branch, changes made, validation actually performed, and whether commits, pushes, or merges have been completed.
