# Network Inspection

Running `iptest` without arguments selects an inspection path, samples local
interface traffic, discovers the IPv4 public egress address, and queries its location
and available IP-source fields. A short HTTP latency and download measurement
runs after the IP requests finish. No account, API key, proxy-port preset, or
global configuration change is required.

The reported path, IP addresses, interface counters, source labels, and speed
sample answer different questions. None supplies an overall IP cleanliness
judgment.

## Automatic Path Selection

The selection order for HTTPS requests is:

1. An explicit `--proxy` argument.
2. `HTTPS_PROXY` / `https_proxy`, then `ALL_PROXY` / `all_proxy`.
3. An enabled system HTTPS or SOCKS proxy supported by the platform adapter.
4. The current system network route.

Uppercase environment variables take precedence over lowercase variants.
The environment is sampled once for the invocation. HTTP-only configuration
does not govern these HTTPS requests unless the platform explicitly shares it
with HTTPS. Explicit proxy selection overrides environment bypass rules.

`Network path` states the selected source without printing proxy credentials.
The system-route description says that it may include TUN/VPN. The tool follows
the operating system's existing routing; it does not establish that traffic is
direct, inspect every private proxy application, or prove which tunnel carries
each destination. A process or listening port alone is not selected as a proxy.

### Platform adapters

| Platform | Settings read | Requirements and limits |
| --- | --- | --- |
| Windows | Current-user settings through `WinHttpGetIEProxyConfigForCurrentUser`; common static proxy, protocol-specific HTTPS and SOCKS entries, bypass list, script and auto-discovery state | Runs as the current user. It does not change WinHTTP or user settings or enumerate another account's configuration. |
| macOS | Global proxy dictionary from `scutil --proxy`, including enabled HTTPS/SOCKS entries and supported exceptions | `scutil` must be available. Per-interface scoped settings are not resolved independently; their presence is disclosed when no global HTTPS/SOCKS proxy is selected. |
| GNOME / Unity | The current desktop's `org.gnome.system.proxy` settings through `gsettings`, including manual protocols, shared HTTP/HTTPS mode, and ignore-hosts | Requires the appropriate current desktop session and `gsettings`. Missing `gsettings` produces a coverage warning and uses the current route; other read/parse failures are errors. |
| KDE / Plasma | `kioslaverc` proxy settings through `kreadconfig6` or `kreadconfig5` | If both readers are absent, the user's configuration file is read with a reduced-coverage warning; system-wide KConfig defaults are not inspected in that fallback. |
| Headless or other Linux desktop | Process proxy environment; otherwise the current route | Unknown desktop sessions disclose that desktop-specific discovery is unavailable. The adapter does not read unrelated stale GNOME/KDE settings. |

Linux desktop selection uses `XDG_CURRENT_DESKTOP`, falling back to
`DESKTOP_SESSION`. Discovery commands are bounded and do not modify the session.
Supported bypass entries include local hosts, domains, literal addresses,
CIDRs, and applicable ports. A matching exception refuses the proxied inspection
request rather than silently sending it directly.

### Disabled, unavailable, and automatic configurations

These states have different outcomes:

| State | Behavior |
| --- | --- |
| No applicable application proxy | Follow the current system route and inspect its actual public exit. |
| Enabled supported proxy | Use it for egress, location, IP-source, and active measurement requests. |
| Enabled invalid configuration or failed settings read | Report the configuration error; do not replace it with a direct connection. |
| Selected proxy cannot connect or authenticate | Preserve the failure and any independent results; never retry through a different route. |
| Explicit PAC script / automatic-script mode | Report unsupported automatic proxy evaluation with exit `2`; `--proxy` can select an intended proxy explicitly. |
| WPAD / automatic discovery enabled without a resolved static proxy | Warn that automatic discovery was not resolved and use the current system route. An automatic proxy may still exist; the output does not claim otherwise. |

The tool currently does not fetch or execute PAC scripts, discover a WPAD
server, or provide a JavaScript proxy evaluator. Windows/macOS auto-discovery
flags and KDE auto-discovery mode are therefore disclosed rather than presented
as successful proxy discovery. Known enabled but invalid static settings and
explicit PAC scripts are not treated as an ordinary proxy-off state.

## Traffic and Active Measurements

### Passive interface traffic

`Interface traffic` is a one-shot sample of byte-counter changes for active,
non-loopback interfaces. The requested wait is one second, and rates use the
actual elapsed interval between reads. Counter-reader startup and execution can
make the invocation longer than one second. This stage runs before this
invocation's outbound inspection or measurement requests.

- Windows reads `Get-NetAdapterStatistics` through a hidden, noninteractive
  `powershell.exe` process. Each read has a five-second limit. The command and
  its NetAdapter module must be available to the current account.
- Linux reads `/proc/net/dev`.
- macOS reads `/usr/sbin/netstat -ibn`, with a three-second command limit.

Counters represent all interface traffic, including other applications. A
physical interface and a TUN interface may observe the same underlying traffic;
the tool lists them separately and never sums them into a bandwidth total.
New, missing, or reset counters are omitted instead of producing negative rates.
A stable interface with no traffic can validly show zero. If counters cannot be
sampled, the stage reports failure while retaining the other inspection stages.

Rates use **KiB/s**, where one KiB is 1,024 bytes. They are current usage, not a
capacity estimate or a measurement of one proxy application's exclusive traffic.
`--no-traffic` skips this sample.

### Active HTTP measurement

The embedded Go engine uses three curated HTTPS targets without registration,
keys, external speed-test programs, or a self-hosted service:

| Order | Target | Protocol |
| --- | --- | --- |
| 1 | Cloudflare: `https://speed.cloudflare.com/__down` | Request a byte count through the `bytes` query parameter |
| 2 | OVHcloud Singapore: `https://sin1-sgp.speedtest.network.ovh.net/files/100Mb.dat` | Read a bounded prefix of the published 100 MiB test file |
| 3 | OVHcloud Roubaix: `https://lil1-rbx.speedtest.network.ovh.net/files/100Mb.dat` | The same static-file protocol, at a different region |

See [Cloudflare's reference project](https://github.com/cloudflare/speedtest) and
[OVHcloud's network tools](https://docs.ovhcloud.com/pt/guides/network/network-tools).
These represent two providers; the two OVHcloud regions are not independent
operators. Public availability and use limits remain under each operator's
control. LibreSpeed and other providers are not integrated in this milestone.

Default `--speedtest auto` tries these targets in order, stopping after the first
complete latency/download result. A failed target is diagnosed on stderr and
does not itself fail the stage when a later target succeeds. No target is retried.
`--speedtest compare` tests every target sequentially and displays each target's
available measurements and status, including failures or budget skips. Comparison
returns partial-failure status if any requested target fails or is skipped. Rates
are neither summed nor averaged. Simultaneous full downloads are not performed.

The current sequence is:

1. Make three tiny requests and take the median completed HTTP request duration
   as **HTTP response latency**, in milliseconds. Cloudflare requests zero bytes;
   static-file targets request a one-byte range for each sample. A latency request
   has a two-second limit, also subject to the inherited HTTP timeout.
2. Request at most **16 MiB** of uncompressed download data, with a **four-second
   download measurement budget**. The inherited per-request timeout still
   applies and can end a request sooner. Each target's entire attempt has an
   eight-second limit, including its latency requests.
3. Calculate received payload bits divided by elapsed request seconds. Print the
   rate in **Mbps**, where one Mbps is 1,000,000 bits per second, together with
   transferred bytes, duration, and the target name.

The entire default stage has a **20-second / 16 MiB** budget. Comparison has a
**30-second / 48 MiB** budget. Received payloads from failed transfers and tiny
latency samples count against the same total byte budget; switching targets does
not reset it. A target's download size is reduced to the remaining budget.
Unstarted targets are marked skipped when the total budget is exhausted. The
printed stage total includes all consumed body bytes, even failed samples.
These are application-read payload limits; HTTP/TLS overhead and transport
buffering can add network traffic. Discovery, IP lookups, and interface sampling
are outside the speed stage's budget.

Static-file requests send `Range: bytes=0-N`. A `206` response must have the exact
matching `Content-Range` and expected declared length when present. A server
ignoring Range may return `200` only with the known full-file length; the engine
reads at most the requested prefix and closes the body without draining the file.
Redirects, HTML/JSON/XML, compression, wrong lengths/ranges, and unsuccessful
HTTP statuses are rejected. Response errors expose only safe status/stage details.

The download duration includes request setup so that a tiny, already-buffered
response does not produce an exaggerated body-only rate. If the measurement's
own time budget ends after receiving valid data, those bytes can form a limited
sample. A failed request, invalid response, canceled invocation, or transfer
without measurable data is not reported as a valid download rate. If latency
completed but the download failed, the valid latency remains visible without a
fabricated zero download speed. Incomplete latency samples are not reported as a
completed median. Caller cancellation, total/target deadlines, and inherited HTTP
timeouts do not become successful partial downloads. This is a
short bounded sample, not a sustained multi-connection saturation test.

HTTP response latency includes HTTP/server processing and can include connection
setup. It is not ICMP ping or a guarantee of latency to another service. There is
no upload, packet-loss, or loaded-latency test in this milestone.

All measurement requests retain the selected HTTP client, proxy policy, and
timeout, with redirects disabled. No direct retry is made after proxy failure.
Nevertheless, a proxy's split-routing or rotating-exit policy can choose
different paths for IP-echo and speed-test hosts. The speed result is attributed
to its own target; it does not prove that every request used the displayed IP or
that the measured rate is an ISP plan limit.

Active measurements run once per invocation after the IP requests finish, not
once per address family. They run only when at least one egress probe succeeded.
`--no-speedtest` skips both the latency probes and the download. The measurement
uses real network traffic, and its service availability depends on the current
network and provider. `--no-speedtest` takes precedence over either selected
mode. No uploads, hidden keys, dynamic server-list downloads, or proxy-setting
changes are performed.

## Terminal Output and Execution Status

Default `--format report` presents Connection, Local network, Public egress,
IP source data, and Network speed sections. Local addresses and sampled rates
share a table; multiple addresses on one interface display rates only once.
An interface with counters but no requested-family address may have `-` for its
address. Missing samples and measurements use `-`, distinct from a measured zero.
Speed targets have separate Download (Mbps), HTTP (ms), and Status columns;
comparison includes failed and skipped targets. Sample payload and duration
remain visible below the table. No final IP-quality judgment is added.

Report output is buffered until inspection returns so stderr progress cannot
interrupt its tables. Cancellation flushes sections already completed. Invalid
arguments/configuration and help/version do not produce an empty report.
`--format plain` preserves the earlier line-oriented result labels for users
who prefer that format. Neither format changes requests, lookup policy, or exit
status. JSON output and interactive full-screen UI are not implemented.

Source information uses plain ASCII borders and Field / Status / Meaning columns.
All three cells wrap, preserving long source fields and counts. Windows console
width is queried natively; Linux/macOS use the terminal window size. The renderer
supports 40–240 cells, with reports capped at 100 for readability. Redirected or
non-console output uses a deterministic width of 100. Below 40 cells is outside
the supported layout range. Ordinary CJK wide characters and combining marks are handled;
East Asian ambiguous characters use one cell.

Only useful fields are included. Explicit source booleans remain `yes`/`no`, zero
device estimates are omitted, and missing or null activity history adds no row.
At most eight sorted event types are displayed, with another row counting any
additional types. These rows carry no local purity verdict or risk grade.

Stage progress and diagnostics go to standard error as plain text. Results go to
standard output. Redirecting output does not introduce ANSI color or cursor
sequences. `--help` and `--version` skip discovery, counters, and network requests.

Default `--family ipv4` inspects only IPv4 and filters local addresses accordingly.
`--family both` adds IPv6; `--family ipv6` requests IPv6 only. Explicit
`--family auto` tries both egress families and keeps the available results. One failed
family alone does not cause failure in auto mode when the other succeeds;
the failed probe is disclosed without asserting that the entire host lacks that
protocol. Explicit `--family both` requires both probes to succeed. Location or
source-query failures, failed requested traffic or active measurements, and
interface-read failures return `1` while preserving successful results. Missing
optional source fields are not failures. Invalid arguments or unsupported/invalid
proxy configuration return `2`; cancellation returns `130`. Family selection
controls the inspected addresses and IP lookups, not the IP version used to
connect to a proxy or a speed target. Interface byte counters are per interface,
without separate IPv4/IPv6 accounting.

`--timeout` is per HTTP request, not an overall command budget. System discovery,
counter reads, the sampling wait, IP lookups, and the bounded speed stage all
contribute to total runtime.

## Runtime Validation

Fixture tests and cross-compilation are development checks. They cannot confirm
that each host exposes the same configuration, that a proxy node is reachable,
or that a provider currently accepts anonymous requests from that exit. Current
optimization-milestone verification is recorded in the README; the following
manual acceptance work remains required on actual target hosts:

| Environment or scenario | Acceptance to verify | Runtime status |
| --- | --- | --- |
| Windows user proxy | Zero-argument selection, disabled/on/invalid states, native path label, counters without visible helper windows | Current configured proxy, both egress families, source tables, and one active interface verified; off/invalid transitions fixture-covered |
| Ubuntu / GNOME | Current-session settings, proxy off/manual/PAC, missing utility diagnostic, counters | Pending target-host acceptance |
| Arch / CachyOS / KDE | KDE reader and user-file fallback, environment precedence, manual/off/PAC modes, counters | Pending target-host acceptance |
| macOS | Global HTTPS/SOCKS selection, scoped-settings warning, counter parsing and console width | Pending target-host acceptance |
| Headless Linux | Environment-only and no-application-proxy paths, no false desktop configuration claim | Pending target-host acceptance |
| TUN/VPN | Actual public exit through the existing route; interfaces shown separately with no combined rate | Pending target-host acceptance |
| Node changes | Public egress changes as expected; location/source queries stay bound to each observed IP | Pending real-node acceptance |
| Family selection | Default IPv4 only; both adds IPv6; explicit auto keeps available results | Default IPv4-only output live-verified; family filtering/concurrency fixture-covered; real IPv4-only network acceptance pending |
| Provider or proxy failure | Clear diagnostics, no hidden direct retry, successful IP/location results retained | Fixture-covered; Windows comparison retained successful OVHcloud Singapore/Roubaix latency/download samples despite Cloudflare EOF; automatic failover fixture-covered |
| Narrow console / redirected output | Sections, local/traffic integration, wrapped tables, usable text, no ANSI/control sequences, completed sections after cancellation | Fixture-covered at 40/60/80/100 cells, long IPv6 and CJK; actual target terminals pending |

## Reviewing and Submitting This Milestone

Keep the related optimizations on `feature/network-optimization` and group
commits by function: terminal rendering, automatic proxy discovery, passive and
active network measurement, CLI integration/report presentation, then documentation. Include each
module's tests with its implementation. This grouping describes the final
implementation and does not require committing historical intermediate designs.

After validation and review, the feature PR targets `develop` and normally uses
squash merge. Updating documentation does not itself authorize a commit, push,
PR, merge, or release.
