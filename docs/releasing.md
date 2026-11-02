# Release Workflow and Cross-platform Acceptance

The project keeps three long-lived branches: `main`, `develop`, and the literal
branch `release`. Individual `feature/*` branches are short-lived. A published
version must provide packages that run on the supported Windows, Linux, and macOS
targets without a Go SDK, compilation, or the repository's development setup.

The current implementation was developed and exercised on Windows. Cross-builds
exist for six OS/CPU targets, but they are not native runtime acceptance for all
targets. A release branch or a version tag does not establish that acceptance.

## Branch Responsibilities

| Branch | Role | Changes accepted |
| --- | --- | --- |
| `main` | Published source history | Approved `release` candidates through regular merge PRs |
| `develop` | Ongoing feature integration | Routine task PRs, regular release-fix synchronization, and published-main synchronization |
| `release` | One frozen candidate at a time | A selected `develop` snapshot, candidate-fix task PRs, and published-main synchronization |
| `feature/*` | Individual implementation/review work | Normally based on `develop`; candidate fixes based on `release` |

Use squash merge for task PRs by default. Use regular merges for every promotion
or synchronization between long-lived branches. Never delete, reset, recreate,
or force-push `release` to begin the next version. Version numbers belong in tags.
Do not delete `develop` or `release` when their promotion PRs are merged.

## Establishing the Persistent Branch

Run these commands only when no local or remote `release` branch exists, with a
clean working tree and an up-to-date `main` baseline:

```sh
git fetch origin --prune
git branch --no-track release origin/main
git push -u origin release
```

If `origin/release` already exists, create a local tracking branch rather than
reinitializing it:

```sh
git fetch origin --prune
git switch --track origin/release
```

If a local `release` branch was already created, inspect its tip and upstream
before pushing or tracking another branch. Initializing from `main` establishes
the published baseline; the first `develop -> release` promotion brings in the
candidate implementation. This bootstrap is not itself a candidate approval.

Configure protection/rulesets for all three long-lived branches, including
deletion and force-push protection. Require the implemented native checks before
promotion/publication. Repository rules must permit regular merges; a linear-only
history requirement conflicts with this workflow. Do not require an approval
from another account when the project is maintained by one person. Add required
check names only after the actual workflows exist and have produced those checks.
These documents do not configure GitHub settings automatically.

## Preparing and Freezing a Candidate

1. Implement release tooling and documentation on a routine task branch such as
   `feature/release-preparation`, targeting `develop`. Routine development stays
   off the three long-lived branches.
2. Finish the previous publication/synchronization cycle. Maintain only one
   active candidate on `release`.
3. Open a PR with base `release` and compare `develop`. Confirm that its head
   commit is the intended snapshot immediately before merging; an open PR tracks
   later changes to its source branch.
4. Merge with **Create a merge commit** and record the resulting candidate SHA.
   Run native tests and build/package acceptance against that candidate.
5. Freeze the candidate. New `develop` work does not automatically enter
   `release`. A candidate change invalidates affected checks and package hashes.

For a candidate defect, branch a new `feature/*` from `release`, target `release`,
and normally squash the task PR. Synchronize accepted fixes back using a regular
`release -> develop` PR, without bringing unrelated `develop` features into the
candidate. Final `main -> develop` synchronization also carries release fixes.

## What Ready-to-run Means

State the supported OS versions, CPU architectures, and required OS utilities.
Readiness applies to that declared matrix, not every historical OS version,
desktop configuration, or unknown network environment.

- Ship the correct native executable; users do not install Go, build source, or
  install an external speed-test program. Retain the project's pure-Go build
  policy and verify any actual runtime dependencies.
- Preserve Unix executable permissions in packaged artifacts and test the
  documented extraction/start commands from paths containing spaces and Unicode.
- Exercise the final downloaded packages, not only binaries in a build directory.
  Verify the embedded version, asset names, CPU target, and SHA-256 checksums.
- Check macOS first launch after an ordinary browser download, including system
  trust checks/quarantine and permissions. Decide the signing/notarization plan
  before claiming an unrestricted first-launch experience. A CI-built binary
  running inside its build job is not that acceptance.
- Check Windows first launch from the downloaded package and document its signing
  status. Running on a build host alone does not establish the download experience.
- Verify the platform-specific tools actually used: Windows PowerShell/NetAdapter,
  Linux counters and applicable GNOME/KDE readers, and macOS `scutil`/`netstat`.
  Document supported headless and desktop paths separately.
- Public IP, geolocation, source facts, and throughput still depend on current
  routes and public services. Verify bounded failures and retained partial results;
  readiness does not mean every remote request always succeeds. Continue to
  disclose unsupported PAC evaluation and unresolved discovery coverage.

## Required Native Matrix

Every OS/CPU target advertised as supported in a formal release needs native
runtime evidence. A successful cross-build or emulation-only check is separate
evidence and must not be relabeled as native acceptance.

| Artifact target | Required native environment | Current evidence |
| --- | --- | --- |
| Windows amd64 | Supported Windows x64 | Development-host tests, earlier live inspection, and local extracted-package/version checks passed; first native CI and browser-download acceptance pending |
| Windows arm64 | Supported Windows ARM64 | Cross-build only; native acceptance pending |
| Linux amd64 | Supported Linux x64 | Cross-build only; native acceptance pending |
| Linux arm64 | Supported Linux ARM64 | Cross-build only; native acceptance pending |
| macOS amd64 | Supported Intel macOS | Cross-build only; native acceptance pending |
| macOS arm64 | Supported Apple Silicon macOS | Cross-build only; native acceptance pending |

The workflow `.github/workflows/native.yml` configures native runners for all six
targets: Windows 2022 x64, Windows 11 ARM64, Ubuntu 24.04 x64/ARM64, and macOS 15
Intel/Apple Silicon. Labels were checked against GitHub's runner reference;
availability and actual job success remain to be verified on GitHub. Record each
run's actual OS image, CPU, SDK, and commit. The workflow provisions `.go-version`
using official `actions/setup-go` on disposable runners; it does not assume the
Windows-only local SDK scripts work on Linux/macOS. Build/module/temp caches stay
under workspace `.cache/go`; runner-managed SDK/telemetry state is ephemeral.

For each target, the required automated checks are:

1. Formatting verification, static analysis, and the full test suite on the
   native OS/CPU, including the platform code selected by build tags.
2. A native application build with release version metadata, followed by
   package creation, extraction, and execution with the development SDK absent
   from the runtime environment. Keep required OS utilities available.
3. Help/version, default IPv4 and explicit family options, report/plain output,
   Unicode/long paths, cancellation/partial results, and a failed selected proxy
   that never triggers direct fallback.
4. Deterministic network fixtures for full successful inspection and speed
   failover/comparison. Anonymous external APIs must not make routine test
   results depend on public rate limits or proxy credentials.
5. Checksums and an acceptance result for the exact package bytes to be uploaded.

Headless CI is not desktop-session acceptance. In addition, record actual host or
VM results for Ubuntu/GNOME, Arch/KDE, CachyOS and its supported desktop, macOS,
and supported Windows user settings. Cover proxy off/static/TUN as applicable,
bypass safety, PAC limitations, counters, and real provider failure behavior.
An Ubuntu runner does not by itself validate Arch/CachyOS desktop integration.
Record actual tested configurations; do not mark pending rows successful.

## Implemented Tooling

`LICENSE` is MIT, copyright 2026 ObsidianLoneCrane. Every package includes this
license and the concise instructions from [install.md](install.md).

`internal/buildinfo` retains `iptest dev` for ordinary development builds.
Release builds display the injected version, commit, and commit-derived UTC build
date. A version stamp is embedded so the packaging tool can validate cross-built
executables even when Go's `-trimpath` build metadata omits linker flags.

On the Windows development host, with a clean source tree:

```powershell
.\scripts\build-release.ps1 -Version v0.1.0-rc.1
```

This uses the pinned local SDK to build all six targets and writes versioned
archives plus `SHA256SUMS` under `dist/v0.1.0-rc.1`. It restores process GOOS/GOARCH
settings afterward and never changes user/system Go settings. Existing version
output directories and archive files are not silently replaced. A failed build
may leave partial output; use a fresh preview version after fixing the failure.

For a native-only candidate, add `-NativeOnly`. For explicit local validation of
uncommitted changes, use a prerelease version and `-AllowDirty`; its embedded
commit is marked dirty and it is not an approved publication artifact.

```powershell
.\scripts\build-release.ps1 -Version v0.1.0-local.1 -NativeOnly -AllowDirty
.\scripts\smoke-package.ps1 -Archive dist/v0.1.0-local.1/iptest_v0.1.0-local.1_windows_amd64.zip -Version v0.1.0-local.1
```

The smoke script checks the archive against `SHA256SUMS`, extracts it into a path
with spaces and Chinese characters, and runs the packaged application with SDK
locations removed from its child PATH. It checks version/help and every family
mode in both output formats using a failed explicit proxy. It makes no public
lookup/speed requests and does not change system proxy settings. It is exercised
on Windows PowerShell 5.1 and PowerShell 7 locally; other target runs remain pending.

`cmd/releasepack` is a maintainer tool, not a user executable. It checks the Go
binary's application path, OS/CPU, CGO setting, and exact injected version before
archiving. Windows uses ZIP; Linux/macOS use tar.gz with executable mode 0755.
Archive headers use stable timestamps. Checksum generation hashes existing
packages rather than rebuilding them; complete manifests require all six targets.

The native workflow runs on task PRs targeting `develop`/`release`/`main`, pushes
to those branches, version-tag pushes, and manual dispatch. Each matrix job runs
format/vet/tests, builds and packages a native binary, and exercises the extracted
package. Only if all six jobs pass does the aggregation job produce the complete
`candidate-VERSION` artifact and checksum manifest. All jobs use read-only
repository permissions. No job publishes a GitHub Release, pushes tags, or
modifies branch protection. CI artifacts are review/download staging, not the
anonymous release links intended for end users.

For non-tag runs, the default version is `v0.0.0-ci.COMMIT`; manual dispatch can
specify a candidate version, and a version-tag run uses that tag. The exact
prepared commit is checked out by all jobs. Tests remain deterministic and do not
depend on public API availability. Desktop configuration, counters on actual
desktops, and browser-download trust checks remain separate acceptance work.

## Packaging and Publishing

Prefer ZIP for Windows and archives preserving executable mode for Linux/macOS.
Include the executable, license, and concise usage instructions. Keep SDKs,
caches, proxy configuration, credentials, and local inspection logs out of assets.
Stage packages under ignored output directories and attach them as release assets.

Once the frozen candidate passes acceptance:

1. Open a PR with base `main`, compare `release`, and link the native CI and
   package/desktop acceptance evidence. Fix failed checks before publishing.
2. Merge with **Create a merge commit**. Confirm the final `main` source tree is
   the approved candidate tree; resolve and revalidate any unexpected differences.
3. Tag that `main` commit. Do not tag whichever branch happens to be checked out
   without checking the commit.
4. Build the final versioned packages from that tag. If promotion or version
   metadata changes the actual package bytes, repeat final-package smoke checks
   and generate checksums for the bytes that will actually be uploaded.
5. Create a draft GitHub Release for the tag, attach every validated package and
   its checksums, review release notes/download instructions, then publish.
   Source-code archives do not replace executable downloads.

A preview/prerelease must disclose incomplete evidence and must not be presented
as satisfying formal cross-platform acceptance. Choosing a version label does
not waive the user's Windows/Linux/macOS ready-to-run requirement for formal
releases. Never claim untested targets are verified.

## Synchronizing for the Next Cycle

After publication, with a clean tree and no newly frozen candidate, use regular
`main -> release` and `main -> develop` synchronization PRs. Fast-forward local
synchronizations are allowed when repository rules permit direct updates. See
[CONTRIBUTING](../CONTRIBUTING.md#synchronizing-after-publication) for commands.
Keep all three long-lived branches and clean up only completed task branches.

## Acceptance Record

For each version, record the candidate SHA, final tag/commit, asset SHA-256 values,
exact OS/CPU versions, CI links, desktop configuration scenarios, package launch
results, and unresolved limitations. Redact actual user IPs and credentials.

Current status: the branch process, MIT license, version metadata, archive/checksum
tools, native CI configuration, and installation instructions are implemented.
Windows extracted-package checks and six-target cross-build/package generation
passed locally. GitHub native jobs have not yet run; other native platform,
desktop, and browser-download acceptance remains pending. No formal publication
has been performed and full cross-platform readiness is not yet established.

## References

- [GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- [Protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
- [Managing releases](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository)
