# iptest

A cross-platform IP inspection command-line tool for personal use. It prioritizes the selected proxy's public egress IP and displays local interface addresses, public egress results, estimated locations, and available third-party IP facts in the terminal.

> Project status: egress inspection, geolocation, and anonymous IP-source lookup. The CLI reads local interface addresses, detects public IPv4/IPv6 egress through an explicit proxy, environment proxy, or the current system route, then queries each observed IP's estimated location and proxycheck.io data in parallel. No registration, API key, email address, or data-source configuration is needed. Source availability is best effort: only useful returned facts are displayed, and failed queries produce a diagnostic without discarding other results. The tool does not assign an overall cleanliness or risk judgment. Saved configuration and desktop system proxy discovery remain planned.

## Goals

- Use the same inspection logic on Windows, Ubuntu, Arch Linux, CachyOS, and macOS.
- Prioritize the selected proxy's actual public egress IP, with support for HTTP CONNECT and SOCKS5.
- Limit location details to country/region, state/province, and city.
- Show useful source facts so the user can judge an IP's suitability, without a cleanliness score, blanket verdict, or local attention threshold.
- Use free, anonymous data sources without requiring registration, API keys, or an email address.
- Consider Android support through Termux in a later phase.

## Output Design

The following example illustrates a successful `--family ipv4` inspection. The addresses and location are sample data, not actual inspection results:

```text
Local interface IP: 192.168.1.20 (Ethernet)
Public egress IP (IPv4): 203.0.113.10
Estimated location (IPv4): Japan / Tokyo Metropolis / Tokyo
IP source data (IPv4, proxycheck.io):
| Field       | Status  | Meaning |
| -----       | ------  | ------- |
| network     | Hosting | Network classification reported by the provider. |
| proxy       | no      | Whether the provider identifies this IP as a proxy. |
| VPN         | no      | Whether the provider identifies this IP as a VPN exit. |
| Tor         | no      | Whether the provider identifies this IP as a Tor exit. |
| hosting     | yes     | Whether the provider flags a hosting/datacenter address. |
| compromised | no      | Whether the provider flags this host as compromised. |
| scraper     | no      | Whether the provider flags scraper activity from this IP. |
```

Output rules:

- Enumerate non-loopback addresses on active interfaces. List multiple interfaces by name; virtual interfaces may be included.
- Check IPv4 and IPv6 separately. The default is `--family both`, which runs both families concurrently and prints results in IPv4/IPv6 order. Each family first detects its egress IP, then queries that exact IP's location and source data concurrently. Use `--family ipv4` or `--family ipv6` to request only one.
- Exclude unspecified, multicast, loopback, and link-local interface addresses. Private LAN and unique-local IPv6 addresses are retained.
- Query geolocation for each observed IP explicitly. Display country/region, state/province, and city in that order, with `Unknown` for each missing level. A successful, matching response with all three levels missing is displayed as `Unknown / Unknown / Unknown` and is not a lookup failure.
- Geolocation estimates the location associated with the observed egress IP. It does not identify a particular person, household, or street address.
- Display an `IP source data` table only when useful provider fields are available. Each protocol family has its own source heading and three columns: Field, Status, and Meaning. The table contains network type, a positive address-level device estimate, explicit detection flags, or positive activity counts. Omit unavailable, invalid, empty, and uninformative fields without adding a verdict or placeholder.
- Write normal results to standard output and errors to standard error. Use exit codes to indicate execution status.
- If a location lookup fails, retain the observed egress IP, show `Unknown (lookup failed)` for location, and write a diagnostic to standard error. If the IP-source lookup fails, omit its line and write a diagnostic to standard error, preserving successful egress and location results. If egress detection fails, skip both lookups, show `Unknown (egress unavailable)` for location, and omit the source-data line for that family.

## Proxy Selection

Current precedence for HTTPS egress probes, geolocation, and IP-source requests:

```text
Command-line --proxy
    > HTTPS_PROXY / https_proxy
    > ALL_PROXY / all_proxy
    > Current system network path
```

Uppercase environment variables take precedence over their lowercase variants. `HTTP_PROXY` / `http_proxy` are supported for HTTP requests, but the current public probes and lookup requests use HTTPS and therefore need `HTTPS_PROXY` or `ALL_PROXY`.

Saved proxy configuration and desktop static proxy discovery are future work. This version does not read Windows desktop proxy settings, GNOME/KDE settings, or macOS system proxy settings. Set `--proxy` or the relevant environment variable when using a proxy that is configured only in a desktop application. Without a selected HTTPS proxy, the CLI writes a short explanation to standard error and uses the current system route, which may include a TUN interface or VPN.

Current behavior and future requirements:

1. Explicitly support proxy environment variables: select `HTTP_PROXY` or `HTTPS_PROXY` according to the request scheme, with `ALL_PROXY` as a fallback. Handle the corresponding lowercase variables and `NO_PROXY` exceptions.
2. Honor a manually selected proxy without allowing environment-based bypass rules to change the inspection path unexpectedly.
3. In automatic mode, refuse a request when `NO_PROXY` / `no_proxy` excludes a target that would otherwise use the selected proxy. Do not issue a direct request or present it as a proxy egress IP. To use that proxy for the target, specify `--proxy` explicitly. PAC configurations are not supported yet.
4. Report connection failures, authentication failures, and timeouts for the selected proxy. Do not silently fall back to a direct connection.
5. If no application-level proxy is found, use the current system network path, which may still traverse a TUN interface or VPN.
6. Use the same HTTP client and proxy policy for both egress families and their geolocation and IP-source requests. Both lookup adapters explicitly query the IP already obtained.
7. Preserve differences when targets or inspection times produce different egress IPs. Do not combine one IP's location with another IP's source data.

HTTP and HTTPS proxies support CONNECT for HTTPS targets. `socks5://` and `socks5h://` both send the target hostname to the proxy in this Go implementation. A correct public IP result does not establish that all system traffic or DNS uses the same path.

A proxy can rotate its outbound IP between requests even when the client and proxy policy stay the same. Both lookups remain bound to the IP observed by the preceding egress probe; the tool does not claim that the lookup connections necessarily used that same outbound IP. A provider denying anonymous proxy requests does not trigger a direct retry.

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

After a successful probe, the CLI requests `https://ipwho.is/{IP}?fields=ip,success,country,region,city`, where `{IP}` is the exact observed egress address. This lookup needs no API key and uses the same HTTP client and proxy policy as the probe. The response must indicate success, follow the expected schema, and identify the queried IP. HTTP failures, rate limits such as `429`, invalid responses, mismatched IPs, and timeouts leave the IP result intact while marking location as unavailable.

The IP-source adapter requests `https://proxycheck.io/v3/{IP}?ver=24-June-2026&tag=0` for that same observed address. It uses anonymous access without a key or email address, through the same HTTP client and proxy policy. The API response version is pinned and query tagging is disabled. No registration or additional data-source configuration is required. Denied access, rate limits, invalid responses, mismatched IPs, and timeouts omit the source-data line and produce a safe diagnostic without retrying through a direct connection. See [IP Source Data](#ip-source-data) for displayed fields and [Data Sources](#data-sources) for anonymous-access limits.

`--timeout` applies to each HTTP request, not the entire inspection. Each family runs an egress probe first, followed by parallel location and IP-source requests; these two waves can together take approximately twice the configured timeout. With `--family both`, the two family pipelines also run concurrently. `--help` and `--version` do not contact any service. There is no automatic retry or cache; each observed address receives at most one IP-source lookup per family per invocation.

Egress responses are limited to 4 KiB, must contain one JSON value with a valid IP of the requested family, and are checked against private and non-public special-purpose ranges. These checks use the [IANA IPv4](https://www.iana.org/assignments/iana-ipv4-special-registry/) and [IPv6 special-purpose registries](https://www.iana.org/assignments/iana-ipv6-special-registry/); they do not establish live route reachability or IP reputation.

Exit codes:

| Code | Meaning |
| --- | --- |
| `0` | Local interface enumeration and all requested egress and lookup requests succeeded; a valid response without useful source fields also succeeds |
| `1` | Runtime failure or partial results, including an unavailable requested address family or a failed lookup |
| `2` | Invalid arguments or proxy configuration |
| `130` | Inspection canceled with Ctrl+C |

With the default `both`, an IPv4-only connection can produce useful IPv4 output and exit `1` because IPv6 failed. Use `--family ipv4` when only IPv4 is required. A failed location or IP-source lookup produces exit `1` while preserving successful results. Missing location levels in an otherwise valid response do not count as failure. Missing source fields, including an otherwise valid all-null report, do not add a source-data line or an insufficient-data error. Returned values and activity counts do not change the exit code; exit codes describe execution status, not whether an IP is acceptable.

Local configuration loading is not implemented or required for these lookups. The local configuration patterns in `.gitignore` reserve filenames for future use; they do not mean that automatic loading is already supported.

## IP Source Data

| Available field | Display rule |
| --- | --- |
| Network type | Show usable source text such as Residential, Business, Wireless, or Hosting; omit placeholders and unsafe labels |
| Address-level device estimate | Show `device_estimate.address` only when it is a valid positive count; never substitute the subnet estimate |
| Detection flags | Show explicit VPN, proxy, Tor, compromised, scraper, and hosting booleans as `yes` or `no`; omit missing or invalid flags |
| Activity history | Show valid positive counts with factual, sanitized event names, including login attempts, registration attempts, form submissions, and other event types |

Each field is independent. Missing, `null`, invalid, or uninformative values are omitted while useful fields remain visible as table rows. Explicit `false` booleans remain visible as `no`; this means the provider did not flag that category, not independent proof of its absence. Empty or null activity history and zero activity counts produce no history rows. If no useful fields remain, the entire heading and table are omitted; a successful request with no useful fields is not itself a runtime failure.

The device estimate is the provider's estimate of devices behind the specific address. It is not the number of people, concurrent users, or devices across the whole subnet. A zero estimate is omitted as uninformative, not presented as proof that nobody uses the IP. No device-count threshold is used to classify the address.

These flags, estimates, and counts are source-provided facts, not measurements independently made by this tool. Activity labels and counts have separate rows without assigning a local abuse or risk category. Event names are sanitized and sorted, with at most eight event types shown; an `other event types` row gives the number of additional types. A login-attempt count, a VPN label, or a device estimate does not become a blanket judgment about the IP.

The provider's aggregate risk score is not displayed or transformed into a cleanliness score. The tool reports no overall cleanliness or risk verdict. The user decides whether the available facts fit their intended use; omitted fields do not imply a clean address or guarantee acceptance by a website.

## Data Sources

| Purpose | Source | Status |
| --- | --- | --- |
| Public egress detection | [ipify](https://www.ipify.org/) | Implemented: HTTPS IP echo, with separate IPv4/IPv6 checks |
| Estimated geolocation | [ipwhois](https://ipwhois.io/documentation) | Implemented: free HTTPS lookup of the observed IP without an API key, returning country, state/province, and city |
| IP-source facts and estimated sharing | [proxycheck.io v3](https://proxycheck.io/api/) | Implemented: anonymous HTTPS lookup of the observed IP, without registration, a key, an email address, or local configuration |

Anonymous proxycheck.io access is best effort. Its published no-key allowance is 100 queries per day, counted by the IP making the request; users sharing a proxy exit can share that allowance. The provider may deny anonymous requests from proxies, and data fields may be `null`. The tool cannot guarantee availability or populated fields and does not bypass the selected proxy to obtain them. Service limits and capabilities follow the provider's current documentation.

The implemented adapters use bounded HTTP requests, validate responses and queried IPs, and report rate limits and lookup failures without silently changing routes. Geolocation handles missing location fields individually. Caching and automatic retries are not implemented. With both address families available, one run makes up to two IP-source lookups. Lookup services receive the queried IP and see the request's current outbound IP; keep proxy credentials and local inspection logs out of version control and redact credentials from errors and logs.

## Platforms and Technology

The implementation uses Go and the standard library, with no external Go modules. Go is a development dependency: users of a compiled `iptest` executable do not need to install Go.

The verification entries below include the source-facts output change: formatting, vet, package tests, and all six builds passed. Windows help/version and failed-proxy source-omission smoke checks also passed. Tests use local fixtures and mocked transports; they do not establish runtime compatibility on other operating systems.

An earlier bounded live source check queried the fixed public IPv4 address `1.1.1.1` through the development host's current system route. The provider returned HTTP 200 with a compatible v3 response; its `null` attack history supplies no activity counts. That request did not discover or query the host's public egress IP. It does not verify the current output change, real proxy/node switching, live IPv6 results, or access from every proxy exit.

| Platform | Planned Deliverable | Verification Status |
| --- | --- | --- |
| Windows | amd64 and arm64 executables | amd64 build, help/version and failed-proxy smoke checks, and local fixture tests passed; arm64 cross-build only |
| Ubuntu | Linux executable for the target CPU architecture | Linux amd64/arm64 cross-builds passed; distribution runtime verification pending |
| Arch Linux | Linux executable for the target CPU architecture | Linux cross-builds passed; distribution runtime verification pending |
| CachyOS | Linux executable for the target CPU architecture | Linux cross-builds passed; distribution runtime verification pending |
| macOS | Intel and Apple Silicon executables | amd64/arm64 cross-builds passed; runtime verification pending |
| Android | Termux as the initial approach | Future evaluation |

The goal is to share Linux builds across distributions with the same CPU architecture, while verifying execution on each distribution. Android requires separate work on interface enumeration, permissions, and proxy behavior.

Local build artifacts are written to the ignored `bin` directory. Builds verified for this output change are `iptest.exe`, `iptest-windows-arm64.exe`, `iptest-linux-amd64`, `iptest-linux-arm64`, `iptest-darwin-amd64`, and `iptest-darwin-arm64`. Linux/macOS binaries copied from Windows may need their executable permission set with `chmod +x` on the target host.

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

The `version` and `env` commands inspect the development environment. Package tests cover the CLI, interface filtering, proxy selection and forwarding, egress responses, and geolocation response handling. Network tests use local fixtures; they do not query the machine's actual public IP or establish access to the live geolocation service.

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
│   ├── egress/     # Separate IPv4/IPv6 public IP probes
│   ├── geolocation/ # Location lookups bound to each observed egress IP
│   └── reputation/ # Anonymous IP-source lookup and useful-fact formatting
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
- Validate key behaviors: no direct fallback after proxy failure, fixed-IP lookups, IPv4/IPv6, parallel lookup isolation, API rate limits, independent source fields, and omission of unavailable data without an aggregate judgment.
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
- [x] Integrate free geolocation lookups for each observed egress IP.
- [x] Verify geolocation response/IP consistency and partial-failure output using local fixtures.
- [x] Integrate anonymous proxycheck.io v3 source data and address-level device estimates without registration or API keys.
- [x] Replace aggregate judgments with useful returned source facts and omit unavailable fields.
- [x] Verify source-field filtering, factual activity output, parallel lookups, and lookup-failure behavior with local fixtures.
- [x] Test proxy failures, NO_PROXY, TLS verification, response validation, rate limits, cancellation, and timeouts using local fixtures.
- [x] Cross-build the egress and geolocation CLI for Windows, Linux, and macOS on amd64/arm64.
- [x] Validate the source-facts output change and rebuild all six platform/architecture targets.
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
