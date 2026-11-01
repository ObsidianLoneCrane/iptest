// Package cli implements the terminal interface independently of the entry point.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ObsidianLoneCrane/iptest/internal/egress"
	"github.com/ObsidianLoneCrane/iptest/internal/geolocation"
	"github.com/ObsidianLoneCrane/iptest/internal/netinfo"
	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
	"github.com/ObsidianLoneCrane/iptest/internal/reputation"
	"github.com/ObsidianLoneCrane/iptest/internal/speedtest"
	"github.com/ObsidianLoneCrane/iptest/internal/traffic"
)

const help = `Usage: iptest [options]

Inspect local interface IP addresses, public egress IP addresses, their
estimated country/region, state/province, and city, and third-party IP source data.

Options:
  --proxy URL       Use an explicit HTTP, HTTPS, SOCKS5, or SOCKS5H proxy.
                    Overrides proxy environment variables and NO_PROXY.
  --family FAMILY   ipv4, ipv6, both, or auto (default: ipv4).
                    Both adds IPv6; auto keeps whichever families are available.
                    Also filters local interface addresses, not transport routing.
  --no-traffic      Skip the one-second interface traffic sample.
  --no-speedtest    Skip the bounded download and HTTP latency measurements.
  --speedtest MODE  auto or compare (default: auto).
                    Auto switches targets until one succeeds; compare tests all
                    targets sequentially. Budgets: 20s/16 MiB or 30s/48 MiB.
  --format FORMAT  report or plain (default: report).
                    Report groups results into tables; plain prints lines.
  --timeout DURATION
                    Per-request timeout, such as 10s (default: 10s).
                    Egress is followed by parallel location/source requests.
                    A family can take approximately twice this timeout.
  --version         Print the development version without network requests.
  --help, -h        Show this help without network requests.

Environment:
  HTTPS_PROXY / https_proxy, then ALL_PROXY / all_proxy, select the proxy
  for HTTPS probes. Uppercase values take precedence over lowercase values.
  Automatic mode refuses to bypass a selected proxy for NO_PROXY targets.
  Enabled system proxy settings are inspected when no environment proxy exists.
  Without an application proxy, the current system route is used; it may
  traverse a TUN interface or VPN. An unavailable proxy is never bypassed.

Exit codes:
  0  Requested inspection and measurement stages succeeded.
     Unavailable optional source fields are omitted.
     Automatic speed tests need one complete target; comparison needs all.
  1  A requested stage failed; partial results may be available.
  2  Invalid arguments or proxy configuration.
  130  Inspection canceled.
`

type detector interface {
	Detect(context.Context, egress.Family) (egress.Observation, error)
}

type geolocator interface {
	Lookup(context.Context, netip.Addr) (geolocation.Location, error)
}

type reputationLookup interface {
	Lookup(context.Context, netip.Addr) (reputation.Report, error)
}

type speedMeter interface {
	Run(context.Context, bool) (speedtest.Report, error)
}

type dependencies struct {
	interfaces func() ([]netinfo.Address, error)
	client     func(string, time.Duration) (*proxy.Client, error)
	detector   func(*http.Client) detector
	geolocator func(*http.Client) geolocator
	reputation func(*http.Client) reputationLookup
	traffic    func(context.Context, time.Duration) ([]traffic.Rate, error)
	speed      func(*http.Client) speedMeter
}

// Run executes one invocation. Only the caller decides whether to exit the process.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, dependencies{
		interfaces: netinfo.List,
		client:     proxy.NewClient,
		detector:   func(client *http.Client) detector { return egress.New(client) },
		geolocator: func(client *http.Client) geolocator { return geolocation.New(client) },
		reputation: func(client *http.Client) reputationLookup { return reputation.New(client) },
		traffic:    traffic.Sample,
		speed:      func(client *http.Client) speedMeter { return speedtest.New(client) },
	})
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("iptest", flag.ContinueOnError)
	// Flag parsing can echo supplied values. Keep proxy credentials out of errors.
	flags.SetOutput(io.Discard)
	proxyURL := flags.String("proxy", "", "proxy URL")
	family := flags.String("family", "ipv4", "address family")
	noTraffic := flags.Bool("no-traffic", false, "skip interface traffic sample")
	noSpeed := flags.Bool("no-speedtest", false, "skip active speed test")
	speedMode := flags.String("speedtest", "auto", "active speed test mode")
	outputFormat := flags.String("format", "report", "output format")
	timeout := flags.Duration("timeout", 10*time.Second, "per-request timeout")
	version := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, help)
			return 0
		}
		fmt.Fprintln(stderr, "iptest: invalid command-line arguments; use --help for usage.")
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "iptest: positional arguments are not supported; use --help for usage.")
		return 2
	}
	if *version {
		fmt.Fprintln(stdout, "iptest dev")
		return 0
	}
	var families []egress.Family
	switch strings.ToLower(strings.TrimSpace(*family)) {
	case "ipv4":
		families = []egress.Family{egress.IPv4}
	case "ipv6":
		families = []egress.Family{egress.IPv6}
	case "auto", "both":
		families = []egress.Family{egress.IPv4, egress.IPv6}
	default:
		fmt.Fprintln(stderr, "iptest: --family must be auto, ipv4, ipv6, or both.")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "iptest: --timeout must be greater than zero.")
		return 2
	}
	mode := strings.ToLower(strings.TrimSpace(*speedMode))
	if mode != "auto" && mode != "compare" {
		fmt.Fprintln(stderr, "iptest: --speedtest must be auto or compare.")
		return 2
	}
	format := strings.ToLower(strings.TrimSpace(*outputFormat))
	if format != "report" && format != "plain" {
		fmt.Fprintln(stderr, "iptest: --format must be report or plain.")
		return 2
	}
	explicitProvided := false
	flags.Visit(func(option *flag.Flag) {
		if option.Name == "proxy" {
			explicitProvided = true
		}
	})
	if explicitProvided && strings.TrimSpace(*proxyURL) == "" {
		fmt.Fprintln(stderr, "iptest: --proxy requires a nonempty proxy URL.")
		return 2
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		fmt.Fprintln(stderr, "iptest: inspection canceled.")
		return 130
	}
	route, err := deps.client(*proxyURL, *timeout)
	if err != nil {
		switch {
		case errors.Is(err, proxy.ErrAutoProxyUnsupported):
			fmt.Fprintln(stderr, "iptest: automatic proxy configuration is enabled but cannot be resolved; use --proxy to select the intended proxy.")
		case errors.Is(err, proxy.ErrDiscovery):
			fmt.Fprintln(stderr, "iptest: system proxy settings could not be read or are invalid; the proxy has not been bypassed.")
		default:
			fmt.Fprintln(stderr, "iptest: invalid proxy configuration; use --help for supported proxy formats.")
		}
		return 2
	}
	defer route.HTTP.CloseIdleConnections()
	view := newOutput(stdout, format)
	defer view.flush()
	view.connection(routeDescription(route))
	for _, warning := range route.Warnings {
		fmt.Fprintf(stderr, "iptest: %s\n", terminalText(warning))
	}

	addresses, interfaceErr := deps.interfaces()
	selectedAddresses := make([]netinfo.Address, 0, len(addresses))
	for _, address := range addresses {
		for _, requested := range families {
			if requested == egress.IPv4 && address.IP.Is4() || requested == egress.IPv6 && address.IP.Is6() {
				selectedAddresses = append(selectedAddresses, address)
				break
			}
		}
	}
	addresses = selectedAddresses
	exitCode := 0
	if interfaceErr != nil {
		fmt.Fprintln(stderr, "iptest: unable to read all local interface addresses.")
		exitCode = 1
	}
	if len(addresses) == 0 {
		exitCode = 1
	}
	var rates []traffic.Rate
	var trafficErr error
	sampled := deps.traffic != nil && !*noTraffic
	if deps.traffic != nil && !*noTraffic {
		fmt.Fprintln(stderr, "iptest: sampling interface traffic before network requests...")
		rates, trafficErr = deps.traffic(ctx, time.Second)
		if trafficErr != nil {
			fmt.Fprintln(stderr, "iptest: interface traffic sample unavailable.")
			exitCode = 1
		}
	}
	view.local(addresses, rates, sampled, trafficErr != nil)
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	if deps.speed != nil {
		fmt.Fprintln(stderr, "iptest: inspecting public egress and IP information...")
	}

	type result struct {
		observation egress.Observation
		err         error
		location    geolocation.Location
		locationErr error
		report      reputation.Report
		reportErr   error
	}
	results := make([]result, len(families))
	service := deps.detector(route.HTTP)
	locations := deps.geolocator(route.HTTP)
	reputations := deps.reputation(route.HTTP)
	var pending sync.WaitGroup
	for index, requested := range families {
		pending.Add(1)
		go func(index int, requested egress.Family) {
			defer pending.Done()
			entry := &results[index]
			entry.observation, entry.err = service.Detect(ctx, requested)
			if entry.err == nil {
				// Both lookups inspect the observed IP independently. A slow or
				// failed source must not suppress the other source's result.
				var lookups sync.WaitGroup
				lookups.Add(2)
				go func() {
					defer lookups.Done()
					entry.location, entry.locationErr = locations.Lookup(ctx, entry.observation.IP)
				}()
				go func() {
					defer lookups.Done()
					entry.report, entry.reportErr = reputations.Lookup(ctx, entry.observation.IP)
				}()
				lookups.Wait()
			}
		}(index, requested)
	}
	pending.Wait()
	successfulFamilies := 0
	for _, entry := range results {
		if entry.err == nil {
			successfulFamilies++
		}
	}
	for index, requested := range families {
		label := "IPv4"
		if requested == egress.IPv6 {
			label = "IPv6"
		}
		entry := results[index]
		if entry.err != nil {
			if strings.EqualFold(strings.TrimSpace(*family), "auto") && successfulFamilies > 0 {
				fmt.Fprintf(stderr, "iptest: %s probe returned no address; another family is available.\n", label)
				continue
			}
			view.egress(label, "Unknown (detection failed)", "Unknown (egress unavailable)")
			fmt.Fprintf(stderr, "iptest: %s egress detection failed: %s\n", label, diagnostic(entry.err))
			exitCode = 1
		} else {
			location := fmt.Sprintf("%s / %s / %s", locationField(entry.location.Country), locationField(entry.location.Region), locationField(entry.location.City))
			if entry.locationErr != nil {
				location = "Unknown (lookup failed)"
				fmt.Fprintf(stderr, "iptest: %s geolocation lookup failed: %s\n", label, diagnostic(entry.locationErr))
				exitCode = 1
			}
			view.egress(label, entry.observation.IP.String(), location)
			if entry.reportErr != nil {
				fmt.Fprintf(stderr, "iptest: %s reputation lookup failed: %s\n", label, reputationDiagnostic(entry.reportErr))
				exitCode = 1
			} else {
				view.source(label, entry.report)
				if entry.report.Warning {
					fmt.Fprintf(stderr, "iptest: %s source proxycheck.io reported a warning.\n", label)
				}
			}
		}
	}
	if successfulFamilies > 0 && deps.speed != nil && !*noSpeed && ctx.Err() == nil {
		fmt.Fprintln(stderr, "iptest: measuring bounded download throughput and HTTP response latency...")
		report, err := deps.speed(route.HTTP).Run(ctx, mode == "compare")
		view.speed(report, mode == "compare", stderr)
		if err != nil {
			fmt.Fprintf(stderr, "iptest: network speed measurement incomplete (%s); IP results are retained.\n", speedDiagnostic(err))
			exitCode = 1
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	return exitCode
}

func printSpeedReport(report speedtest.Report, compare bool, stdout, stderr io.Writer) {
	for _, attempt := range report.Attempts {
		measurement := attempt.Result
		target := terminalText(measurement.Target)
		if attempt.Err != nil {
			fmt.Fprintf(stderr, "iptest: speed target %s: %s\n", target, speedDiagnostic(attempt.Err))
		}
		// Default output omits failed targets without useful data. Compare lists
		// every configured target, including skipped and partially measured ones.
		if !compare && !measurement.LatencyMeasured && !measurement.DownloadMeasured {
			continue
		}
		var fields []string
		if measurement.DownloadMeasured {
			fields = append(fields, fmt.Sprintf("download %.2f Mbps", measurement.DownloadMbps))
		}
		if measurement.LatencyMeasured {
			fields = append(fields, fmt.Sprintf("HTTP response latency %.1f ms", float64(measurement.Latency)/float64(time.Millisecond)))
		}
		if compare || attempt.Err != nil {
			status := "OK"
			if attempt.Skipped {
				status = "Skipped (total budget exhausted)"
			} else if attempt.Err != nil {
				status = "Failed"
			}
			fields = append(fields, "status "+status)
		}
		fmt.Fprintf(stdout, "Network speed (%s): %s\n", target, strings.Join(fields, "; "))
		if measurement.DownloadMeasured {
			fmt.Fprintf(stdout, "Speed sample: %d bytes over %.2f s; target-specific estimate\n", measurement.Bytes, measurement.Duration.Seconds())
		}
	}
	if len(report.Attempts) > 0 {
		fmt.Fprintf(stdout, "Speed test total: %d payload bytes read over %.2f s (including failed attempts)\n", report.Bytes, report.Duration.Seconds())
	}
}

func speedDiagnostic(err error) string {
	if errors.Is(err, speedtest.ErrBudgetExhausted) {
		return "total measurement budget exhausted"
	}
	message := reputationDiagnostic(err)
	var status *speedtest.HTTPError
	if errors.As(err, &status) {
		message = fmt.Sprintf("HTTP %d", status.StatusCode)
	} else if errors.Is(err, speedtest.ErrInvalidResponse) {
		message = "invalid measurement response"
	}
	var failure *speedtest.Failure
	if errors.As(err, &failure) {
		return failure.Stage + ": " + message
	}
	return message
}

func routeDescription(route *proxy.Client) string {
	if route.Detail != "" {
		return terminalText(route.Detail)
	}
	switch route.Source {
	case proxy.SourceExplicit:
		return "explicit proxy"
	case proxy.SourceEnvironment:
		return "environment proxy"
	case proxy.SourceSystemProxy:
		return "system proxy"
	default:
		return "system route (may include TUN/VPN)"
	}
}

func reputationFailure(err error) string {
	switch {
	case errors.Is(err, reputation.ErrDenied):
		return "anonymous access denied"
	case errors.Is(err, reputation.ErrRateLimited):
		return "rate limited"
	default:
		return "lookup failed"
	}
}

func reputationDiagnostic(err error) string {
	switch {
	case errors.Is(err, proxy.ErrProxyBypass):
		return proxy.ErrProxyBypass.Error()
	case errors.Is(err, context.Canceled):
		return "request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "request timed out"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed before a complete response"
	case errors.Is(err, reputation.ErrDenied), errors.Is(err, reputation.ErrRateLimited):
		return reputationFailure(err)
	default:
		// Do not display transport errors or provider response text here.
		return "request failed"
	}
}

func diagnostic(err error) string {
	if errors.Is(err, proxy.ErrProxyBypass) {
		return proxy.ErrProxyBypass.Error()
	}
	return terminalText(err.Error())
}

func locationField(value string) string {
	value = strings.TrimSpace(terminalText(value))
	if value == "" {
		return "Unknown"
	}
	return value
}

func terminalText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
}
