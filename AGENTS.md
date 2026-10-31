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
- Normal output contains only local interface IP addresses, public egress IP addresses, country/state or province/city, and a brief reputation assessment.
- Prioritize the selected proxy. Do not silently fall back to a direct connection after a proxy failure. Once an egress IP is obtained, explicitly query that same IP with subsequent data sources.
- Display "Unknown" when reputation data is insufficient or a lookup fails. Do not interpret missing data as a clean reputation.
- Do not present unfinished features or planned commands as implemented, or cross-compilation results as successful runtime verification.
- Keep the implementation small, favor the standard library and pure Go dependencies, and organize code according to actual needs.

## Changes and Validation

- Use clear commit prefixes such as `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, and `chore:`. Keep each change focused on one task.
- Use UTF-8 for documentation and follow `.gitattributes`. Check documentation content and diffs when making documentation changes.
- Once the Go module exists, run `gofmt` on modified Go files and perform applicable `go vet ./...`, `go test ./...`, and platform checks according to the changes.
- Do not introduce unrelated tests or create a Go module early for documentation-only changes.
- Keep credentials and local configuration out of version control, and redact sensitive information from logs.
- When finishing a task, report the current branch, changes made, validation actually performed, and whether commits, pushes, or merges have been completed.
