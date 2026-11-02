# Contributing

This project is a cross-platform Go command-line tool. It keeps `main`, `develop`, and `release` as long-lived branches and uses short-lived `feature/*` branches for individual tasks. See [README.md](README.md) for scope and [Releasing](docs/releasing.md) for candidate promotion and cross-platform acceptance.

## Branch Responsibilities

| Branch | Purpose | Rules |
| --- | --- | --- |
| `main` | Published versions | Accept approved candidates from `release`; retain this branch after release PRs |
| `develop` | Integration for ongoing development | Accept routine task branches and release-fix synchronization; retain this branch after promotion PRs |
| `release` | One frozen release candidate at a time | Accept a selected `develop` snapshot, candidate fixes, and synchronization from `main`; retain this branch between versions |
| `feature/*` | Individual development tasks | Normally branch from `develop` and merge back; candidate fixes branch from and target `release` |

Use `feature/*` for features, fixes, refactoring, documentation, tests, and tool configuration changes. Examples:

```text
feature/init-go-cli
feature/proxy-detection
feature/fix-proxy-timeout
feature/docs-branch-workflow
```

Use lowercase English letters, digits, and hyphens in task branch names. Keep each task branch focused and retire it after integration. The literal branch `release` is persistent; version-specific `release/*` and `hotfix/*` branches are not used. Versions are recorded by tags, not by replacing or renaming the persistent branch.

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

The egress, geolocation, and reputation milestones include application packages and local fixture tests. Run package checks through the launcher. Network tests use loopback servers and mocked transports; they do not need a public IP query, a live provider request, or real proxy credentials. Successful local tests do not establish that a particular external proxy or provider is reachable.

To remove the temporary environment, preview `.\scripts\clean-go.ps1 -WhatIf`, then run `.\scripts\clean-go.ps1`. Cleanup removes only `.tools/go` and `.cache/go`, preserving source files and `bin` outputs. Re-run setup when the environment is needed again.

The current environment scripts support Windows only. Linux/macOS setup is future work and must be implemented and validated separately. Android support remains outside the current setup scope. See [README.md](README.md#workspace-local-development-environment) for the full environment and cleanup guide.

The native GitHub Actions workflow is a separate, disposable-runner environment:
official `actions/setup-go` provisions `.go-version`, jobs invoke that SDK directly,
and build/module/temp state stays under workspace `.cache/go`. SDK/telemetry state
on the temporary runner does not persist on the development host. This CI-only
allowance does not replace the Windows project-local launcher. Native CI must run
before its configured checks are described as passing.

Release candidates can be packaged on the Windows development host with
`scripts/build-release.ps1`; extracted native packages are checked with
`scripts/smoke-package.ps1`. See [Releasing](docs/releasing.md#implemented-tooling)
for commands, version metadata, supported archive formats, and remaining acceptance.

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
- For the IP-source lookup, use local proxycheck.io v3 fixtures and the existing proxy-aware HTTP client; no registration, key, email address, or live lookup is needed for tests. Validate the exact observed IP, response schema, bounded bodies, denied anonymous access, rate limits, timeouts, and cancellation. Reject ambiguous IP results and duplicate or case-equivalent JSON keys. An access denial must not cause a retry through a direct connection.
- Keep source fields independent: show a recognized network type, positive address-level device estimate, explicit detection booleans, and valid positive activity counts. Omit missing, null, invalid, empty, and uninformative fields while retaining useful fields. Both true and false detection flags are displayed as yes/no; zero device estimates and zero or empty history produce no text. Subnet counts are never a fallback for an address estimate. The device estimate is not a count of people or concurrent users.
- Test factual tables with Field, Status, and Meaning columns, without aggregate judgments, purity scores, attention thresholds, or required-field completeness rules. Include neutral login/registration/form counts and sanitized unknown event names without assigning local risk categories. Verify sorted event rows capped at eight types, with an additional row counting other event types. Do not display or transform the provider's aggregate risk score. A valid report with no useful fields must omit the entire source-data heading and table without an insufficient-data diagnostic or failure status; available negative detection flags must remain visible.
- For orchestration, run geolocation and the IP-source lookup concurrently only after egress succeeds, querying the same observed IP. Verify that either lookup can fail without suppressing the other's result, failed egress starts neither lookup, output remains in family order, and cancellation returns `130`. A failed source query omits its stdout line, reports a safe diagnostic to stderr, and returns `1` while preserving other results. A successful but unpopulated source response does not itself fail. There are no automatic retries or caches; keep at most one IP-source lookup per observed IP/family per invocation.
- For automatic path discovery, test environment precedence, enabled/off/invalid platform settings, supported bypass rules, PAC rejection, unresolved-discovery warnings, and missing-utility behavior through injected settings/command fixtures. Do not read the developer's actual settings in unit tests. A current-system-route result may include TUN/VPN and must not be labeled as proven direct traffic.
- For traffic counters, use platform-output fixtures and deterministic counter changes. Test elapsed sampling, cancellation, interface resets/removals, and per-interface output without summing physical and tunnel interfaces. Sampling must precede this invocation's outbound requests.
- For active measurements, use mocked transports or local servers. Verify the existing selected transport, each backend's tiny latency requests and download protocol, shared byte/time budgets including failed transfers, response validation, cancellation, safe errors, and absence of direct fallback. Cover automatic target switching, stopping after success, serial comparison, budget skips, and retaining completed latency when download fails. Comparison reports individual failures; successful automatic failover does not fail the stage. Keep the target and units explicit; do not call the result ICMP ping, exclusive proxy usage, or an ISP-capacity guarantee. `--no-speedtest` overrides either speed mode; `--no-traffic` skips interface sampling.
- For terminal output, verify default grouped report sections and explicit plain-format compatibility, native ASCII borders, wrapping of every column, supported width bounds, CJK/combining display widths, and deterministic redirected output. Preserve available facts and completed sections on cancellation; omit empty source sections and skipped stages. Show interface rates once per interface and distinguish missing measurements from zero. Keep stderr progress out of report tables. Cover IPv4-only defaults for both local addresses and public probes, explicit IPv6/both, and available-family `auto` behavior. Family selection must not claim to force the proxy or speed transport's IP version. See [Network inspection](docs/network-inspection.md) for platform requirements and the manual acceptance matrix; cross-builds do not complete those runtime checks.
- For platform integration, build for the target platform and perform necessary runtime checks. Successful cross-compilation does not mean the program has been verified on that system.
- Label unimplemented features as planned. Do not claim that tests or platform checks passed unless they were actually performed.

## Merging Feature Branches into develop

1. Push the current task branch.
2. Create a GitHub PR with **base set to `develop` and compare set to the current `feature/*` branch**. Do not use the default `main` target for routine tasks.
3. Review the change scope, validation results, and unresolved issues. For this personal project, the maintainer may review their own changes; approval from another account is not required.
4. Select **Squash and merge** by default to integrate one task as a single clear commit.
5. After merging, start new work on a new feature branch from the latest `develop`. Do not continue reusing a branch that has already been squash-merged.

A fix needed by an active frozen candidate is the exception: branch a new
`feature/*` from the latest `release`, target `release`, and normally squash that
task PR. Keep unrelated `develop` work out of the candidate. Synchronize accepted
candidate fixes back through a regular `release -> develop` PR; a final
`main -> develop` synchronization also carries accepted release fixes. Do not
cherry-pick or duplicate the same fix by default. Revalidate the changed candidate.

Local merges must follow the same source, target, and validation requirements. When a shared feature branch needs an updated base, prefer merging the latest `develop` into it rather than rewriting shared history.

Before deleting a task branch, confirm that its PR was merged and that the source branch contains no additional unmerged commits. A squash merge does not preserve the source commits' ancestry, so the success or failure of `git branch -d` alone does not establish whether the content was integrated. Do not automatically force-delete branches.

## Promoting develop into release

1. Finish the previous release cycle and confirm that no other candidate is active.
2. Choose the intended `develop` snapshot and review its changes against `release`.
3. Open a promotion PR with **base `release`, compare `develop`**. Before merging,
   confirm the head commit is the intended snapshot; later `develop` commits can
   change an open PR's contents.
4. Use **Create a merge commit**. Record the resulting `release` commit as the
   candidate and run the required native platform and package checks.
5. Keep the candidate frozen while `develop` continues. Accept only candidate
   fixes through task PRs; do not automatically merge new features from `develop`.

Initialize `release` once from the published baseline on `main` if it does not
already exist, then publish it with its own upstream. Do not recreate it for each
version or make it track `origin/main`. See [Releasing](docs/releasing.md) for the
bootstrap commands and the acceptance requirements that are still pending.

## Publishing release into main

1. Complete the cross-platform acceptance in [Releasing](docs/releasing.md) for
   every OS/CPU combination included in the release. A Windows cross-build alone
   does not establish Linux/macOS readiness.
2. Open the publishing PR with **base `main`, compare `release`**.
3. Use **Create a merge commit**. Do not squash or rebase this long-lived branch.
4. Verify that the final source tree on `main` matches the approved candidate,
   create the version tag on that `main` commit, and verify the final packaged
   artifacts before publishing their release assets and checksums.
5. Retain `main`, `release`, and `develop`; clean up only completed task branches.

All promotions and synchronizations between long-lived branches use regular
merges. This preserves the shared history across repeated release cycles.
Creating a tag or GitHub Release does not authorize bypassing platform checks.

## Synchronizing after publication

With a clean working tree and no new active candidate, sync the published commit
back into both long-lived branches:

```sh
git fetch origin --prune
git switch main
git pull --ff-only origin main
git switch release
git pull --ff-only origin release
git merge --no-edit origin/main
git push origin release
git switch develop
git pull --ff-only origin develop
git merge --no-edit origin/main
git push origin develop
```

Allow fast-forward merges when possible; use regular merges when branches have
diverged. If remote rules require PRs, submit `main -> release` and
`main -> develop` synchronization PRs instead of pushing merges directly. Resolve
and validate conflicts; do not reset, recreate, or force-push a long-lived branch
as a substitute for synchronization.

## Multiple Machines and Repository Conventions

- Use the same GitHub repository across machines. Before switching machines, commit and push work that needs to continue. On the other machine, fetch first, then switch to and update the appropriate branch.
- If `git pull --ff-only` fails, inspect the divergence and the origin of the commits before proceeding. Do not default to a force push.
- Do not force-push or rewrite history on `main`, `develop`, `release`, or other shared branches.
- Keep API keys, proxy credentials, local configuration, and runtime logs out of commits. Commit only configuration templates that contain no real credentials.
- These documents define development conventions; they do not automatically enable GitHub branch protection or rulesets. Remote rules are determined by the actual GitHub configuration.
- Protect all three long-lived branches against deletion and force pushes. Keep them when a promotion PR is merged. Do not enable automatic PR-head deletion without ensuring these branches are protected; task branches are the intended cleanup targets. Required native CI checks can be enforced after the workflows exist and have run; do not name unimplemented checks as if they were active.

## References

- [GitHub PR merge methods](https://docs.github.com/en/pull-requests/reference/pull-request-merges)
- [Git merge](https://git-scm.com/docs/git-merge)
- [Git branch](https://git-scm.com/docs/git-branch)
