# Contributing

This project is a Go command-line tool for personal use. It uses `main`, `develop`, and `feature/*` branches to keep tasks focused and commits concise. See [README.md](README.md) for the project scope and feature boundaries.

## Branch Responsibilities

| Branch | Purpose | Rules |
| --- | --- | --- |
| `main` | Stable releases | Accept verified versions from `develop`; do not perform routine development directly on this branch |
| `develop` | Integrate changes for the next version | Accept task branches; do not perform routine development directly on this branch |
| `feature/*` | Individual development tasks | Branch from the latest `develop` and merge back when complete |

Use `feature/*` for features, fixes, refactoring, documentation, tests, and tool configuration changes. Examples:

```text
feature/init-go-cli
feature/proxy-detection
feature/fix-proxy-timeout
feature/docs-branch-workflow
```

Use lowercase English letters, digits, and hyphens in branch names. Keep each branch focused on one task and retire it when that task is complete. Additional branch types such as `release/*` and `hotfix/*` are not used at this stage.

## Starting a Task

Check the current branch and working tree first. Identify any existing uncommitted changes before proceeding. Preserve user changes; do not automatically discard, overwrite, or hide them.

If `develop` has not been published to the remote, run this once:

```sh
git push -u origin develop
```

Once the working tree is clean and the remote `develop` exists, use the following workflow. This example updates the README; replace the branch name for the actual task:

```sh
git status
git fetch origin --prune
git switch develop
git pull --ff-only origin develop
git switch -c feature/update-readme
```

When starting on another machine, clone the repository first. If there is no local `develop`, fetch the remote branches and run `git switch --track origin/develop` to create the corresponding tracking branch.

If an existing unmerged `feature/*` branch is suitable for the current task, update and continue using it instead of creating another branch for the same task.

## Development Environment

Use the repository's temporary Go environment for all development work. It is a development dependency; users of a compiled `iptest` executable will not need Go installed.

The current scripts support Windows PowerShell 5.1 and PowerShell 7 on Windows. From the repository root, initialize the pinned SDK and inspect the environment:

```powershell
.\scripts\setup-go.ps1
.\scripts\go.ps1 version
.\scripts\go.ps1 env GOROOT GOPATH GOCACHE GOMODCACHE GOTMPDIR GOENV GOTOOLCHAIN GOTELEMETRY
```

The first setup downloads the official SDK and verifies its SHA-256 checksum. `.go-version` pins the development SDK to `1.27.3`; the `go 1.26.0` directive in `go.mod` sets the module's minimum version and does not select the development SDK.

Keep SDK downloads and extracted tools under `.tools/go`, and Go workspace/cache/configuration/temporary state under `.cache/go`. Both directories are ignored by Git. The launcher temporarily sets `GOROOT`, `GOPATH`, `GOCACHE`, `GOMODCACHE`, `GOTMPDIR`, `APPDATA`, `LOCALAPPDATA`, `TEMP`, and `TMP`, with `GOENV=off` and `GOTOOLCHAIN=local`; the Go child process inherits these settings, and `finally` restores the previous process environment. It does not write user-level or system-level environment settings. Do not install a global Go toolchain, persistently change `PATH` or `HOME`, or use `go env -w` for this project.

Disable telemetry with the official `go telemetry off` command using the project-local configuration paths. `GOTELEMETRY` is queried for verification, not set as an environment variable to control telemetry.

Invoke Go through `.\scripts\go.ps1`; do not bypass it with a system `go` executable. Once source packages exist, for example:

```powershell
.\scripts\go.ps1 fmt ./...
.\scripts\go.ps1 build ./...
.\scripts\go.ps1 vet ./...
.\scripts\go.ps1 test ./...
```

The egress and geolocation milestones include application packages and local fixture tests. Run package checks through the launcher. Network tests use loopback servers and do not need a public IP query, a live geolocation request, or real proxy credentials; successful local tests do not establish that a particular external proxy or provider is reachable.

To remove the temporary environment, preview `.\scripts\clean-go.ps1 -WhatIf`, then run `.\scripts\clean-go.ps1`. Cleanup removes only `.tools/go` and `.cache/go`, preserving source files and `bin` outputs. Re-run setup when the environment is needed again.

The current environment scripts support Windows only. Linux/macOS setup is future work and must be implemented and validated separately. Android support remains outside the current setup scope. See [README.md](README.md#workspace-local-development-environment) for the full environment and cleanup guide.

When updating the development SDK, update `.go-version` and the matching entries in `scripts/go-checksums.json` together. Verify the hashes against the [official Go downloads](https://go.dev/dl/) before running setup. Do not automatically select a different SDK or relax checksum verification when a pinned archive is missing.

## Commits and Validation

Review the actual diff before committing and stage only files related to the task. Continuing with the README example:

```sh
git status
git diff
git add README.md
git diff --cached
git commit -m "docs: update project documentation"
git push -u origin feature/update-readme
```

Use commit messages in the form `type: short description`, with prefixes such as `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, and `chore:`. Describe what changed rather than using a vague message consisting only of `update` or `fix`.

Match validation to the scope of the change:

- For documentation changes, check content, links, examples, encoding, line endings, and `git diff --check`.
- Once source packages exist, format modified Go files with `gofmt` through `.\scripts\go.ps1 fmt ./...`. Run applicable `.\scripts\go.ps1 vet ./...` and `.\scripts\go.ps1 test ./...` checks for source changes, using the same project-local environment for builds.
- For network logic, focus on preventing direct fallback after proxy failure, keeping the inspected IP fixed, IPv4/IPv6 behavior, and API error handling.
- For geolocation, check that requests use the observed IP and existing HTTP client, that returned IPs match, and that missing location levels remain valid `Unknown` values. Cover malformed responses, rate limits, cancellation, and timeouts with local fixtures; failures must preserve successful egress results and produce partial-failure status. A failed egress probe must not start a geolocation lookup.
- For platform integration, build for the target platform and perform necessary runtime checks. Successful cross-compilation does not mean the program has been verified on that system.
- Label unimplemented features as planned. Do not claim that tests or platform checks passed unless they were actually performed.

## Merging Feature Branches into develop

1. Push the current task branch.
2. Create a GitHub PR with **base set to `develop` and compare set to the current `feature/*` branch**. Do not use the default `main` target for routine tasks.
3. Review the change scope, validation results, and unresolved issues. For this personal project, the maintainer may review their own changes; approval from another account is not required.
4. Select **Squash and merge** by default to integrate one task as a single clear commit.
5. After merging, start new work on a new feature branch from the latest `develop`. Do not continue reusing a branch that has already been squash-merged.

Local merges must follow the same source, target, and validation requirements. When a shared feature branch needs an updated base, prefer merging the latest `develop` into it rather than rewriting shared history.

Before deleting a task branch, confirm that its PR was merged and that the source branch contains no additional unmerged commits. A squash merge does not preserve the source commits' ancestry, so the success or failure of `git branch -d` alone does not establish whether the content was integrated. Do not automatically force-delete branches.

## Merging develop into main

1. Update `develop` and confirm the intended release changes and validation results.
2. Create a release PR with **base set to `main` and compare set to `develop`**.
3. Use **Create a merge commit**, a regular merge. Do not squash-merge or rebase-merge the long-lived `develop` branch.
4. After the release merge, sync `main` back into `develop` before starting the next round of tasks.

A regular merge preserves the shared history of `main` and `develop`, preventing previously integrated commits from reappearing after repeated squash merges of a long-lived branch.

With a clean working tree, sync the release changes as follows:

```sh
git fetch origin --prune
git switch main
git pull --ff-only origin main
git switch develop
git pull --ff-only origin develop
git merge --no-edit origin/main
git push origin develop
```

Allow fast-forward merges when possible; use a regular merge when the branches have diverged. If remote rules require PRs to update `develop`, create a `main -> develop` synchronization PR and merge it with a regular merge instead. Resolve and validate conflicts before proceeding; do not reset or recreate `develop` as a substitute for synchronization.

## Multiple Machines and Repository Conventions

- Use the same GitHub repository across machines. Before switching machines, commit and push work that needs to continue. On the other machine, fetch first, then switch to and update the appropriate branch.
- If `git pull --ff-only` fails, inspect the divergence and the origin of the commits before proceeding. Do not default to a force push.
- Do not force-push or rewrite history on `main`, `develop`, or other shared branches.
- Keep API keys, proxy credentials, local configuration, and runtime logs out of commits. Commit only configuration templates that contain no real credentials.
- These documents define development conventions; they do not automatically enable GitHub branch protection or rulesets. Remote rules are determined by the actual GitHub configuration.

## References

- [GitHub PR merge methods](https://docs.github.com/en/pull-requests/reference/pull-request-merges)
- [Git merge](https://git-scm.com/docs/git-merge)
- [Git branch](https://git-scm.com/docs/git-branch)
