# iptest

A cross-platform IP inspection command-line tool for personal use. It prioritizes the selected proxy's public egress IP and displays local interface addresses and egress results in the terminal. Estimated location and brief reputation lookups are planned for subsequent milestones.

> Project status: initial egress inspection. The CLI reads local interface addresses and detects public IPv4/IPv6 egress through an explicit proxy, environment proxy, or the current system route. Geolocation, reputation lookups, saved configuration, and desktop system proxy discovery remain planned. Missing features are displayed as `Unknown`, not successful checks.

## Goals

- Use the same inspection logic on Windows, Ubuntu, Arch Linux, CachyOS, and macOS.
- Prioritize the selected proxy's actual public egress IP, with support for HTTP CONNECT and SOCKS5.
- Limit location details to country/region, state/province, and city.
- Summarize IP reputation in plain language without inventing an unsupported "IP cleanliness percentage."
- Start with free data sources and allow personal API keys for additional reputation checks.
- Consider Android support through Termux in a later phase.

## Output Design

The following example illustrates a successful `--family ipv4` inspection in the current milestone. Addresses are sample data, not actual inspection results:

```text
Local interface IP: 192.168.1.20 (Ethernet)
Public egress IP (IPv4): 203.0.113.10
Estimated location (IPv4): Unknown (not implemented)
IP reputation (IPv4): Unknown (not implemented)
```

Output rules:

- Enumerate non-loopback addresses on active interfaces. List multiple interfaces by name; virtual interfaces may be included.
- Check IPv4 and IPv6 separately. The default is `--family both`, which runs the two probes concurrently and prints results in IPv4/IPv6 order. Use `--family ipv4` or `--family ipv6` to request only one.
- Exclude unspecified, multicast, loopback, and link-local interface addresses. Private LAN and unique-local IPv6 addresses are retained.
- Show geolocation and reputation as `Unknown (not implemented)` until those lookups are added. Future lookups will use each observed IP explicitly.
- Display "Unknown" for missing location levels. Geolocation estimates the location of the IP's network egress.
- Write normal results to standard output and errors to standard error. Use exit codes to indicate execution status.
- If a location or reputation lookup fails, retain any IP addresses already obtained and display "Unknown" with a brief reason for the failed field.

## Proxy Selection

Current precedence for HTTPS egress probes:

```text
Command-line --proxy
    > HTTPS_PROXY / https_proxy
    > ALL_PROXY / all_proxy
    > Current system network path
```

Uppercase environment variables take precedence over their lowercase variants. `HTTP_PROXY` / `http_proxy` are supported for HTTP requests, but the current public probes use HTTPS and therefore need `HTTPS_PROXY` or `ALL_PROXY`.

Saved proxy configuration and desktop static proxy discovery are future work. This version does not read Windows desktop proxy settings, GNOME/KDE settings, or macOS system proxy settings. Set `--proxy` or the relevant environment variable when using a proxy that is configured only in a desktop application. Without a selected HTTPS proxy, the CLI writes a short explanation to standard error and uses the current system route, which may include a TUN interface or VPN.

Current behavior and future requirements:

1. Explicitly support proxy environment variables: select `HTTP_PROXY` or `HTTPS_PROXY` according to the request scheme, with `ALL_PROXY` as a fallback. Handle the corresponding lowercase variables and `NO_PROXY` exceptions.
2. Honor a manually selected proxy without allowing environment-based bypass rules to change the inspection path unexpectedly.
3. In automatic mode, refuse a request when `NO_PROXY` / `no_proxy` excludes a target that would otherwise use the selected proxy. Do not issue a direct request or present it as a proxy egress IP. To use that proxy for the target, specify `--proxy` explicitly. PAC configurations are not supported yet.
4. Report connection failures, authentication failures, and timeouts for the selected proxy. Do not silently fall back to a direct connection.
5. If no application-level proxy is found, use the current system network path, which may still traverse a TUN interface or VPN.
6. Use the same client and proxy policy for both egress families. Future geolocation and reputation requests must explicitly query the IP already obtained.
7. Preserve differences when targets or inspection times produce different egress IPs. Do not combine one IP's location with another IP's reputation.

HTTP and HTTPS proxies support CONNECT for HTTPS targets. `socks5://` and `socks5h://` both send the target hostname to the proxy in this Go implementation. A correct public IP result does not establish that all system traffic or DNS uses the same path.

## Build and Run

Build with the project-local development environment on Windows:

```powershell
.\scripts\go.ps1 build -o bin/iptest.exe ./cmd/iptest
.\bin\iptest.exe --help
```

On a host with a suitable Go environment, the application can be built with `go build -o bin/iptest ./cmd/iptest`. The repository's temporary environment scripts currently support Windows only.

The following examples assume the executable is on `PATH`:

```sh
# Select the inspection path automatically
iptest

# Explicitly select an HTTP proxy
iptest --proxy http://127.0.0.1:7890

# Explicitly select a SOCKS5 proxy
iptest --proxy socks5://127.0.0.1:1080

# Inspect only IPv4, with a per-request timeout
iptest --proxy http://127.0.0.1:7890 --family ipv4 --timeout 5s

# Inspect only IPv6
iptest --family ipv6

# Print help or the development version without network requests
iptest --help
iptest --version
```

To run from the current directory, use `.\iptest.exe` on Windows or `./iptest` on Linux/macOS.

The current endpoints are `https://api.ipify.org?format=json` for IPv4 and `https://api6.ipify.org?format=json` for IPv6. A failed or unavailable IPv6 probe remains separate from a successful IPv4 result; the program does not substitute IPv4 for IPv6.

Responses are limited to 4 KiB, must contain one JSON value with a valid IP of the requested family, and are checked against private and non-public special-purpose ranges. These checks use the [IANA IPv4](https://www.iana.org/assignments/iana-ipv4-special-registry/) and [IPv6 special-purpose registries](https://www.iana.org/assignments/iana-ipv6-special-registry/); they do not establish live route reachability or IP reputation.

Exit codes:

| Code | Meaning |
| --- | --- |
| `0` | All requested egress families and local interface enumeration succeeded |
| `1` | Runtime failure or partial results, including an unavailable requested address family |
| `2` | Invalid arguments or proxy configuration |
| `130` | Inspection canceled with Ctrl+C |

With the default `both`, an IPv4-only connection can produce useful IPv4 output and exit `1` because IPv6 failed. Use `--family ipv4` when only IPv4 is required. Location and reputation placeholders do not change the exit status in this milestone.

The local configuration and API key formats will be defined when the configuration module is implemented. The local configuration patterns in `.gitignore` reserve filenames for future use; they do not mean that automatic loading is already supported.

## IP Reputation Results

| Result | Meaning |
| --- | --- |
| No known risk records found | Successfully queried reputation sources currently show no relevant risk records |
| Risk detected | A queried source returned risk evidence, such as abuse reports; include a brief reason |
| Unknown | Reputation checks are not configured, a service is unavailable, or the available data is insufficient |

"No known risk records found" does not guarantee that every website will accept the IP. VPN/proxy use or a hosting-network classification alone does not establish abuse.

## Data Sources

| Purpose | Source | Status |
| --- | --- | --- |
| Public egress detection | [ipify](https://www.ipify.org/) | Implemented: HTTPS IP echo, with separate IPv4/IPv6 checks |
| Estimated geolocation | [ipwhois](https://ipwhois.io/documentation) | Planned: free endpoint without an API key for country, state/province, and city |
| Abuse reputation | [AbuseIPDB](https://docs.abuseipdb.com/) | Planned: lookup endpoints using the user's own free API key |

Use separate adapters for data sources and handle timeouts, caching, rate limits, and missing fields. Service limits and capabilities follow each provider's current documentation. Lookup services receive the queried IP; keep API keys and proxy credentials in local configuration and redact them from logs.

## Platforms and Technology

The implementation uses Go and the standard library, with no external Go modules. Go is a development dependency: users of a compiled `iptest` executable do not need to install Go.

| Platform | Planned Deliverable | Verification Status |
| --- | --- | --- |
| Windows | amd64 and arm64 executables | amd64 build, offline CLI smoke checks, and local fixture tests passed; arm64 cross-build only |
| Ubuntu | Linux executable for the target CPU architecture | Linux amd64/arm64 cross-builds passed; distribution runtime verification pending |
| Arch Linux | Linux executable for the target CPU architecture | Linux cross-builds passed; distribution runtime verification pending |
| CachyOS | Linux executable for the target CPU architecture | Linux cross-builds passed; distribution runtime verification pending |
| macOS | Intel and Apple Silicon executables | amd64/arm64 cross-builds passed; runtime verification pending |
| Android | Termux as the initial approach | Future evaluation |

The goal is to share Linux builds across distributions with the same CPU architecture, while verifying execution on each distribution. Android requires separate work on interface enumeration, permissions, and proxy behavior.

Local build artifacts are written to the ignored `bin` directory. Builds verified in this milestone are `iptest.exe`, `iptest-windows-arm64.exe`, `iptest-linux-amd64`, `iptest-linux-arm64`, `iptest-darwin-amd64`, and `iptest-darwin-arm64`. Linux/macOS binaries copied from Windows may need their executable permission set with `chmod +x` on the target host.

## Workspace-local Development Environment

Use a temporary Go environment inside this repository instead of installing a global toolchain. The current scripts support Windows PowerShell 5.1 and PowerShell 7 on Windows. Run the examples below from the repository root.

### Set up the toolchain

```powershell
.\scripts\setup-go.ps1
.\scripts\go.ps1 version
```

The first setup requires an internet connection. It downloads the Go SDK from the official Go distribution service and verifies the archive's SHA-256 checksum before use. The SDK version is pinned to **Go 1.27.3** in `.go-version`. The `go 1.26.0` directive in `go.mod` declares the module's minimum Go language/toolchain requirement; it is separate from the SDK version selected for this development environment.

If local script execution is blocked, use a separate PowerShell process with a temporary execution policy:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\setup-go.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\go.ps1 version
```

This changes the policy only for that process. It does not change the user's or machine's execution policy.

The scripts keep their downloaded SDK and Go state in these ignored directories:

| Directory | Contents |
| --- | --- |
| `.tools/go` | Downloaded SDK archives and the extracted Go toolchain |
| `.cache/go` | Project-local Go workspace, build cache, module cache, and temporary files |

### Run Go commands

Always invoke Go through the project launcher:

```powershell
.\scripts\go.ps1 version
.\scripts\go.ps1 env GOROOT GOPATH GOCACHE GOMODCACHE GOTMPDIR GOENV GOTOOLCHAIN GOTELEMETRY
```

Use the same launcher for formatting, building, and validation:

```powershell
.\scripts\go.ps1 fmt ./...
.\scripts\go.ps1 build ./...
.\scripts\go.ps1 vet ./...
.\scripts\go.ps1 test ./...
```

The `version` and `env` commands inspect the development environment. Package tests exercise the CLI, interface filtering, proxy selection and forwarding, and egress response handling. Network tests use local fixtures and do not query the machine's actual public IP.

For each invocation, the launcher temporarily configures its process environment and lets the Go child process inherit it. A `finally` block restores the previous values when the invocation finishes. It does not write user-level or system-level environment settings.

| Setting | Value |
| --- | --- |
| `GOROOT` | The SDK directory under `.tools/go` |
| `GOPATH`, `GOBIN`, `GOCACHE`, `GOMODCACHE`, `GOTMPDIR` | Dedicated directories under `.cache/go` |
| `APPDATA`, `LOCALAPPDATA`, `TEMP`, `TMP` | Dedicated directories under `.cache/go`, keeping Go configuration and temporary state project-local |
| `GOENV` | `off`, so the user's persistent Go environment file is not used |
| `GOTOOLCHAIN` | `local`, so Go does not automatically download another toolchain |
| `GOWORK`, `GO111MODULE` | `off` and `on`, respectively, to use the project's module independently |
| `GOFLAGS`, `GOCACHEPROG` | Cleared for the invocation to avoid inherited build overrides or external cache helpers |
| `CGO_ENABLED` | `0`, keeping this project's default development workflow pure Go |

Telemetry is disabled with the official `go telemetry off` command and its configuration is stored inside the project-local environment. `GOTELEMETRY` in the `env` example is a read-only status query, not an environment variable used to disable telemetry.

No global Go installation, persistent `PATH` update, or `HOME` change is required. Do not use `go env -w` to configure this project; keep its environment in the launcher.

The current environment scripts support Windows only. Equivalent setup for Linux and macOS is future work, separate from the planned application's cross-platform Go implementation. This setup does not establish Android support or runtime compatibility for the planned application.

### Remove the temporary environment

Finish running Go commands before cleanup. Preview the targets before removing the downloaded toolchain and its caches:

```powershell
.\scripts\clean-go.ps1 -WhatIf
```

Then remove the project-local environment:

```powershell
.\scripts\clean-go.ps1
```

Cleanup targets only `.tools/go` and `.cache/go`. Source code, tracked project files, and application binaries in `bin` are retained. Run `setup-go.ps1` again to restore the development environment when needed.

To remove only caches while retaining the SDK, use `.\scripts\clean-go.ps1 -CacheOnly`.

## Current Files

```text
iptest/
├── README.md       # Scope, technical plan, and development progress
├── CONTRIBUTING.md # Branching, commits, validation, and merge workflow
├── AGENTS.md       # Project rules for coding agents
├── go.mod          # Module path and minimum Go version
├── .go-version     # SDK version used by the project-local launcher
├── cmd/iptest/     # Executable entry point
├── internal/
│   ├── cli/        # Arguments, output, orchestration, and exit codes
│   ├── netinfo/    # Local interface address enumeration
│   ├── proxy/      # Explicit/environment proxy selection and transport
│   └── egress/     # Separate IPv4/IPv6 public IP probes
├── scripts/
│   ├── setup-go.ps1 # Download and verify the pinned SDK
│   ├── go.ps1       # Run Go with a project-local environment
│   ├── go-common.ps1 # Shared toolchain paths and environment settings
│   ├── go-checksums.json # Reviewed official SDK archive checksums
│   └── clean-go.ps1 # Remove only the temporary SDK and Go state
├── .gitignore      # Build artifacts and local configuration exclusions
└── .gitattributes  # Cross-platform line-ending rules
```

## Development Conventions

- The module path is `github.com/ObsidianLoneCrane/iptest`, with a minimum Go version of `1.26.0`. The development SDK is pinned separately in `.go-version`.
- Use the project-local Go launcher for development and validation. Keep the SDK under `.tools/go` and its cache/temporary state under `.cache/go`; do not install or configure a global Go environment for this project.
- Use `main`, `develop`, and `feature/*` branches. Start routine changes on a feature branch from the latest `develop`; see [Contributing](CONTRIBUTING.md).
- Use UTF-8 for source files, LF line endings for regular text, and `gofmt` for Go code.
- Keep the implementation small. Separate command entry points, proxy handling, egress detection, geolocation, and reputation logic as needed.
- Validate key behaviors: no direct fallback after proxy failure, egress changes, IPv4/IPv6, API rate limits, and unknown reputation results.
- Keep business logic independent of terminal presentation to support future platform integration.
- Make small commits, each focused on one clear change. Use prefixes such as `docs:`, `feat:`, `fix:`, `test:`, and `chore:`.
- Commit only shareable source code, documentation, and configuration templates. Keep local API keys, proxy credentials, and inspection logs out of version control.

Run these checks through the project launcher:

```powershell
.\scripts\go.ps1 vet ./...
.\scripts\go.ps1 test ./...
```

## Roadmap

- [x] Define the scope and output format for a personal CLI.
- [x] Create the README, Git ignore rules, and line-ending conventions.
- [x] Initialize the Git repository and complete the first push to GitHub.
- [x] Define the `main`, `develop`, and `feature/*` branching and merge workflow.
- [x] Initialize the Go module.
- [x] Verify SDK setup, compilation, environment restoration, and cleanup previews on the Windows development host.
- [x] Implement the CLI command entry point.
- [x] Implement interface enumeration, manual proxies, and proxy environment variables.
- [x] Implement separate IPv4/IPv6 public egress detection.
- [ ] Implement platform-specific static proxy discovery.
- [ ] Integrate free geolocation lookups.
- [ ] Add optional API keys for reputation lookups.
- [x] Test proxy failures, NO_PROXY, TLS verification, response validation, rate limits, cancellation, and timeouts using local fixtures.
- [x] Cross-build the egress CLI for Windows, Linux, and macOS on amd64/arm64.
- [ ] Verify real proxy/node changes and target-system runtime behavior.
- [ ] Verify behavior and build executables for the target systems.
- [ ] Evaluate Android support through Termux.

## Repository and Branch Workflow

Repository: [ObsidianLoneCrane/iptest](https://github.com/ObsidianLoneCrane/iptest). Git initialization and the first push are complete.

| Branch | Purpose | Updates |
| --- | --- | --- |
| `main` | Stable releases | Merge verified versions from `develop` |
| `develop` | Integration for ongoing development | Accept completed `feature/*` branches and sync `main` after releases |
| `feature/*` | Individual features, fixes, documentation, and tests | Branch from the latest `develop` and merge back when complete |

```text
develop -> feature/task-name -> develop -> main
                                 ^          |
                                 |__________|
                                 Sync after release
```

Use squash merge by default for `feature/* -> develop`. Use a regular merge for `develop -> main` to preserve the history of long-lived branches.

See [CONTRIBUTING.md](CONTRIBUTING.md) for starting tasks, syncing across machines, checking commits, and merging changes. Coding agents must also follow the root [AGENTS.md](AGENTS.md).
