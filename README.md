# iptest

A cross-platform IP inspection command-line tool for personal use. It prioritizes the proxy's public egress IP and displays local interface IP addresses, the public egress IP, its estimated location, and a brief reputation assessment in the terminal.

> Project status: requirements and documentation setup. The repository does not yet contain Go source code, a `go.mod` file, or an executable. The inspection commands, proxy integration, and platform support described below are planned and are not yet available to run.

## Goals

- Use the same inspection logic on Windows, Ubuntu, Arch Linux, CachyOS, and macOS.
- Prioritize the selected proxy's actual public egress IP, with support for HTTP CONNECT and SOCKS5.
- Limit location details to country/region, state/province, and city.
- Summarize IP reputation in plain language without inventing an unsupported "IP cleanliness percentage."
- Start with free data sources and allow personal API keys for additional reputation checks.
- Consider Android support through Termux in a later phase.

## Output Design

The following example illustrates the output format. The addresses and location are sample data, not actual inspection results:

```text
Local interface IP: 192.168.1.20 (Ethernet)
Public egress IP: 203.0.113.10
Estimated location: Japan / Tokyo Metropolis / Tokyo
IP reputation: No known risk records found
```

Output rules:

- Enumerate non-loopback addresses on active interfaces. List multiple interfaces by name; virtual interfaces may be included.
- Check IPv4 and IPv6 separately and show the location and reputation associated with each observed egress IP.
- Display "Unknown" for missing location levels. Geolocation estimates the location of the IP's network egress.
- Write normal results to standard output and errors to standard error. Use exit codes to indicate execution status.
- If a location or reputation lookup fails, retain any IP addresses already obtained and display "Unknown" with a brief reason for the failed field.

## Proxy Selection

Planned precedence:

```text
Command-line --proxy
    > Saved tool configuration (if configured)
    > Proxy environment variables
    > Recognized static system proxy settings
    > Current system network path
```

Behavior:

1. Explicitly support proxy environment variables: select `HTTP_PROXY` or `HTTPS_PROXY` according to the request scheme, with `ALL_PROXY` as a fallback. Handle the corresponding lowercase variables and `NO_PROXY` exceptions.
2. Honor a manually selected proxy without allowing environment-based bypass rules to change the inspection path unexpectedly.
3. In automatic mode, report when a bypass rule causes a target to skip the selected proxy. Do not present a direct connection's result as a proxy egress IP. Ask the user to specify a proxy for complex PAC configurations or system settings that are not yet supported.
4. Report connection failures, authentication failures, and timeouts for the selected proxy. Do not silently fall back to a direct connection.
5. If no application-level proxy is found, use the current system network path, which may still traverse a TUN interface or VPN.
6. Apply the same proxy policy to egress detection and subsequent lookups. Once an egress IP is obtained, explicitly query that IP in both geolocation and reputation requests.
7. Preserve differences when targets or inspection times produce different egress IPs. Do not combine one IP's location with another IP's reputation.

Static system proxy settings require platform-specific integration. The Go standard library does not automatically cover all desktop proxy settings, and `ALL_PROXY` requires explicit handling.

## Planned Commands

The following interface is planned and will become usable after implementation. These examples assume the executable is on `PATH`:

```sh
# Select the inspection path automatically
iptest

# Explicitly select an HTTP proxy
iptest --proxy http://127.0.0.1:7890

# Explicitly select a SOCKS5 proxy
iptest --proxy socks5://127.0.0.1:1080
```

To run from the current directory, use `.\iptest.exe` on Windows or `./iptest` on Linux/macOS.

The local configuration and API key formats will be defined when the configuration module is implemented. The local configuration patterns in `.gitignore` reserve filenames for future use; they do not mean that automatic loading is already supported.

## IP Reputation Results

| Result | Meaning |
| --- | --- |
| No known risk records found | Successfully queried reputation sources currently show no relevant risk records |
| Risk detected | A queried source returned risk evidence, such as abuse reports; include a brief reason |
| Unknown | Reputation checks are not configured, a service is unavailable, or the available data is insufficient |

"No known risk records found" does not guarantee that every website will accept the IP. VPN/proxy use or a hosting-network classification alone does not establish abuse.

## Candidate Data Sources

| Purpose | Candidate | Planned Integration |
| --- | --- | --- |
| Public egress detection | [ipify](https://www.ipify.org/) | HTTPS IP echo, with separate IPv4/IPv6 checks |
| Estimated geolocation | [ipwhois](https://ipwhois.io/documentation) | Free endpoint without an API key for country, state/province, and city |
| Abuse reputation | [AbuseIPDB](https://docs.abuseipdb.com/) | Lookup endpoints using the user's own free API key |

Use separate adapters for data sources and handle timeouts, caching, rate limits, and missing fields. Service limits and capabilities follow each provider's current documentation. Lookup services receive the queried IP; keep API keys and proxy credentials in local configuration and redact them from logs.

## Platforms and Technology

The planned implementation uses Go, favoring the standard library and pure Go dependencies to minimize runtime and system-library requirements.

| Platform | Planned Deliverable | Verification Status |
| --- | --- | --- |
| Windows | `iptest.exe` for the target CPU architecture | Not yet implemented or verified |
| Ubuntu | Linux executable for the target CPU architecture | Not yet implemented or verified |
| Arch Linux | Linux executable for the target CPU architecture | Not yet implemented or verified |
| CachyOS | Linux executable for the target CPU architecture | Not yet implemented or verified |
| macOS | Intel and Apple Silicon executables | Not yet implemented or verified |
| Android | Termux as the initial approach | Future evaluation |

The goal is to share Linux builds across distributions with the same CPU architecture, while verifying execution on each distribution. Android requires separate work on interface enumeration, permissions, and proxy behavior.

## Current Files

```text
iptest/
├── README.md       # Scope, technical plan, and development progress
├── CONTRIBUTING.md # Branching, commits, validation, and merge workflow
├── AGENTS.md       # Project rules for coding agents
├── .gitignore      # Build artifacts and local configuration exclusions
└── .gitattributes  # Cross-platform line-ending rules
```

## Development Conventions

- The Go version will be declared in `go.mod`; the planned module path is `github.com/ObsidianLoneCrane/iptest`.
- Use `main`, `develop`, and `feature/*` branches. Start routine changes on a feature branch from the latest `develop`; see [Contributing](CONTRIBUTING.md).
- Use UTF-8 for source files, LF line endings for regular text, and `gofmt` for Go code.
- Keep the implementation small. Separate command entry points, proxy handling, egress detection, geolocation, and reputation logic as needed.
- Validate key behaviors: no direct fallback after proxy failure, egress changes, IPv4/IPv6, API rate limits, and unknown reputation results.
- Keep business logic independent of terminal presentation to support future platform integration.
- Make small commits, each focused on one clear change. Use prefixes such as `docs:`, `feat:`, `fix:`, `test:`, and `chore:`.
- Commit only shareable source code, documentation, and configuration templates. Keep local API keys, proxy credentials, and inspection logs out of version control.

Run these checks once the Go module and source code exist. They are not applicable during the current documentation phase:

```sh
go vet ./...
go test ./...
```

## Roadmap

- [x] Define the scope and output format for a personal CLI.
- [x] Create the README, Git ignore rules, and line-ending conventions.
- [x] Initialize the Git repository and complete the first push to GitHub.
- [x] Define the `main`, `develop`, and `feature/*` branching and merge workflow.
- [ ] Initialize the Go module and command entry point.
- [ ] Implement interface enumeration, manual proxies, and proxy environment variables.
- [ ] Implement platform-specific static proxy support and public egress detection.
- [ ] Integrate free geolocation lookups.
- [ ] Add optional API keys for reputation lookups.
- [ ] Verify proxy failures, network changes, rate limits, and other key scenarios.
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
