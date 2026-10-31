// Package cli implements the terminal interface independently of the entry point.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ObsidianLoneCrane/iptest/internal/egress"
	"github.com/ObsidianLoneCrane/iptest/internal/netinfo"
	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
)

const help = `Usage: iptest [options]

Inspect local interface IP addresses and public egress IP addresses.
Location and reputation lookups are planned; this version reports them as Unknown.

Options:
  --proxy URL       Use an explicit HTTP, HTTPS, SOCKS5, or SOCKS5H proxy.
                    Overrides proxy environment variables and NO_PROXY.
  --family FAMILY   ipv4, ipv6, or both (default: both).
  --timeout DURATION
                    Per-request timeout, such as 10s (default: 10s).
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
  0  All requested egress families and local interface enumeration succeeded.
  1  Inspection failed or only partial results are available.
  2  Invalid arguments or proxy configuration.
  130  Inspection canceled.
`

type detector interface {
	Detect(context.Context, egress.Family) (egress.Observation, error)
}

type dependencies struct {
	interfaces func() ([]netinfo.Address, error)
	client     func(string, time.Duration) (*proxy.Client, error)
	detector   func(*http.Client) detector
}

// Run executes one invocation. Only the caller decides whether to exit the process.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, dependencies{
		interfaces: netinfo.List,
		client:     proxy.NewClient,
		detector:   func(client *http.Client) detector { return egress.New(client) },
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
	}
	results := make([]result, len(families))
	service := deps.detector(route.HTTP)
	var pending sync.WaitGroup
	for index, requested := range families {
		pending.Add(1)
		go func(index int, requested egress.Family) {
			defer pending.Done()
			results[index].observation, results[index].err = service.Detect(ctx, requested)
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
			message := terminalText(entry.err.Error())
			if errors.Is(entry.err, proxy.ErrProxyBypass) {
				message = proxy.ErrProxyBypass.Error()
			}
			fmt.Fprintf(stderr, "iptest: %s egress detection failed: %s\n", label, message)
			exitCode = 1
		} else {
			fmt.Fprintf(stdout, "Public egress IP (%s): %s\n", label, entry.observation.IP)
		}
		fmt.Fprintf(stdout, "Estimated location (%s): Unknown (not implemented)\n", label)
		fmt.Fprintf(stdout, "IP reputation (%s): Unknown (not implemented)\n", label)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	return exitCode
}

func terminalText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
}
