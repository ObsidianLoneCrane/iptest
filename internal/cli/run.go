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
)

const help = `Usage: iptest [options]

Inspect local interface IP addresses, public egress IP addresses, their
estimated country/region, state/province, and city, and third-party IP source data.

Options:
  --proxy URL       Use an explicit HTTP, HTTPS, SOCKS5, or SOCKS5H proxy.
                    Overrides proxy environment variables and NO_PROXY.
  --family FAMILY   ipv4, ipv6, or both (default: both).
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
  Desktop system proxy discovery is not implemented in this version.
  Without an explicit or environment proxy, the current system route is used;
  that route may still traverse a TUN interface or VPN.

Exit codes:
  0  Requested probes/lookups and interface reads succeeded.
     Unavailable optional source fields are omitted.
  1  A request or interface read failed; partial results may be available.
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

type dependencies struct {
	interfaces func() ([]netinfo.Address, error)
	client     func(string, time.Duration) (*proxy.Client, error)
	detector   func(*http.Client) detector
	geolocator func(*http.Client) geolocator
	reputation func(*http.Client) reputationLookup
}

// Run executes one invocation. Only the caller decides whether to exit the process.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, dependencies{
		interfaces: netinfo.List,
		client:     proxy.NewClient,
		detector:   func(client *http.Client) detector { return egress.New(client) },
		geolocator: func(client *http.Client) geolocator { return geolocation.New(client) },
		reputation: func(client *http.Client) reputationLookup { return reputation.New(client) },
	})
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("iptest", flag.ContinueOnError)
	// Flag parsing can echo supplied values. Keep proxy credentials out of errors.
	flags.SetOutput(io.Discard)
	proxyURL := flags.String("proxy", "", "proxy URL")
	family := flags.String("family", "both", "address family")
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
	case "both":
		families = []egress.Family{egress.IPv4, egress.IPv6}
	default:
		fmt.Fprintln(stderr, "iptest: --family must be ipv4, ipv6, or both.")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "iptest: --timeout must be greater than zero.")
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
		fmt.Fprintln(stderr, "iptest: invalid proxy configuration; use --help for supported proxy formats.")
		return 2
	}
	defer route.HTTP.CloseIdleConnections()
	if route.Source == proxy.SourceSystemRoute {
		fmt.Fprintln(stderr, "iptest: using the current system network path. Desktop proxy settings are not read in this version; select a proxy with --proxy or HTTPS_PROXY/ALL_PROXY when needed.")
	}

	addresses, interfaceErr := deps.interfaces()
	exitCode := 0
	if interfaceErr != nil {
		fmt.Fprintln(stderr, "iptest: unable to read all local interface addresses.")
		exitCode = 1
	}
	if len(addresses) == 0 {
		fmt.Fprintln(stdout, "Local interface IP: Unknown (no usable address found)")
		exitCode = 1
	} else {
		for _, address := range addresses {
			fmt.Fprintf(stdout, "Local interface IP: %s (%s)\n", address.IP, terminalText(address.Interface))
		}
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
	for index, requested := range families {
		label := "IPv4"
		if requested == egress.IPv6 {
			label = "IPv6"
		}
		entry := results[index]
		if entry.err != nil {
			fmt.Fprintf(stdout, "Public egress IP (%s): Unknown (detection failed)\n", label)
			fmt.Fprintf(stderr, "iptest: %s egress detection failed: %s\n", label, diagnostic(entry.err))
			fmt.Fprintf(stdout, "Estimated location (%s): Unknown (egress unavailable)\n", label)
			exitCode = 1
		} else {
			fmt.Fprintf(stdout, "Public egress IP (%s): %s\n", label, entry.observation.IP)
			if entry.locationErr != nil {
				fmt.Fprintf(stdout, "Estimated location (%s): Unknown (lookup failed)\n", label)
				fmt.Fprintf(stderr, "iptest: %s geolocation lookup failed: %s\n", label, diagnostic(entry.locationErr))
				exitCode = 1
			} else {
				fmt.Fprintf(stdout, "Estimated location (%s): %s / %s / %s\n", label,
					locationField(entry.location.Country), locationField(entry.location.Region), locationField(entry.location.City))
			}
			if entry.reportErr != nil {
				fmt.Fprintf(stderr, "iptest: %s reputation lookup failed: %s\n", label, reputationDiagnostic(entry.reportErr))
				exitCode = 1
			} else {
				if summary := reputation.Summary(entry.report); summary != "" {
					fmt.Fprintf(stdout, "IP source data (%s, proxycheck.io):\n%s\n", label, summary)
				}
				if entry.report.Warning {
					fmt.Fprintf(stderr, "iptest: %s source proxycheck.io reported a warning.\n", label)
				}
			}
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	return exitCode
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
