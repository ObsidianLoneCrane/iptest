package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/egress"
	"github.com/ObsidianLoneCrane/iptest/internal/netinfo"
	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
)

type detectorFunc func(context.Context, egress.Family) (egress.Observation, error)

func (function detectorFunc) Detect(ctx context.Context, family egress.Family) (egress.Observation, error) {
	return function(ctx, family)
}

func successfulDependencies() dependencies {
	return dependencies{
		interfaces: func() ([]netinfo.Address, error) {
			return []netinfo.Address{{Interface: "Ethernet", IP: netip.MustParseAddr("192.168.1.20")}}, nil
		},
		client: func(string, time.Duration) (*proxy.Client, error) {
			return &proxy.Client{HTTP: &http.Client{}, Source: proxy.SourceExplicit}, nil
		},
		detector: func(*http.Client) detector {
			return detectorFunc(func(_ context.Context, family egress.Family) (egress.Observation, error) {
				ip := "203.0.113.10"
				if family == egress.IPv6 {
					ip = "2001:db8::10"
				}
				return egress.Observation{Family: family, IP: netip.MustParseAddr(ip)}, nil
			})
		},
	}
}

func TestHelpAndVersionDoNotInspectNetwork(t *testing.T) {
	for _, argument := range []string{"--help", "-h", "--version"} {
		t.Run(argument, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			// Nil dependencies ensure these paths do not access interfaces or the network.
			code := run(context.Background(), []string{argument}, &stdout, &stderr, dependencies{})
			if code != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestInvalidArgumentsDoNotInspectOrExposeValues(t *testing.T) {
	secret := "credential-that-must-not-appear"
	for _, args := range [][]string{
		{"--family", "invalid"},
		{"--timeout", "0s"},
		{"--timeout", "-1s"},
		{"--timeout", secret},
		{"--proxy="},
		{"--proxy"},
		{"--unknown=" + secret},
		{secret},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), args, &stdout, &stderr, dependencies{})
			if code != 2 || stdout.Len() != 0 || stderr.Len() == 0 || strings.Contains(stderr.String(), secret) {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestInvalidProxyIsNotEchoedAndStopsInspection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	secret := "http://username:private-password@invalid"
	code := run(context.Background(), []string{"--proxy", secret}, &stdout, &stderr, dependencies{
		client: func(string, time.Duration) (*proxy.Client, error) {
			return nil, errors.New("unsafe upstream diagnostic: " + secret)
		},
	})
	if code != 2 || stdout.Len() != 0 || strings.Contains(stderr.String(), "private-password") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestBothFamiliesAreConcurrentAndOutputIsOrdered(t *testing.T) {
	deps := successfulDependencies()
	ipv6Started := make(chan struct{})
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(ctx context.Context, family egress.Family) (egress.Observation, error) {
			ip := "203.0.113.10"
			if family == egress.IPv6 {
				close(ipv6Started)
				ip = "2001:db8::10"
			} else {
				select {
				case <-ipv6Started:
				case <-ctx.Done():
					return egress.Observation{}, ctx.Err()
				}
			}
			return egress.Observation{Family: family, IP: netip.MustParseAddr(ip)}, nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := run(ctx, nil, &stdout, &stderr, deps)
	text := stdout.String()
	index4 := strings.Index(text, "Public egress IP (IPv4): 203.0.113.10")
	index6 := strings.Index(text, "Public egress IP (IPv6): 2001:db8::10")
	if code != 0 || stderr.Len() != 0 || index4 < 0 || index6 <= index4 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, text, stderr.String())
	}
	if strings.Count(text, "IP reputation (") != 2 || strings.Count(text, "Estimated location (") != 2 || strings.Contains(text, "No known risk") {
		t.Fatalf("unimplemented lookups must stay Unknown: %q", text)
	}
}

func TestSingleFamilyAndOptionsAreForwarded(t *testing.T) {
	deps := successfulDependencies()
	deps.client = func(explicit string, timeout time.Duration) (*proxy.Client, error) {
		if explicit != "socks5://127.0.0.1:1080" || timeout != 3*time.Second {
			t.Errorf("proxy = %q, timeout = %v", explicit, timeout)
		}
		return &proxy.Client{HTTP: &http.Client{}, Source: proxy.SourceExplicit}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--family", "ipv4", "--timeout", "3s", "--proxy", "socks5://127.0.0.1:1080"}, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), "IPv6") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestPartialEgressFailurePreservesSuccessfulResult(t *testing.T) {
	deps := successfulDependencies()
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(_ context.Context, family egress.Family) (egress.Observation, error) {
			if family == egress.IPv6 {
				return egress.Observation{}, errors.New("IPv6 unavailable")
			}
			return egress.Observation{Family: family, IP: netip.MustParseAddr("203.0.113.10")}, nil
		})
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, &stdout, &stderr, deps)
	if code != 1 || !strings.Contains(stdout.String(), "Public egress IP (IPv4): 203.0.113.10") || !strings.Contains(stdout.String(), "Public egress IP (IPv6): Unknown") || !strings.Contains(stderr.String(), "IPv6") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestInterfaceFailureStillDetectsEgress(t *testing.T) {
	deps := successfulDependencies()
	deps.interfaces = func() ([]netinfo.Address, error) {
		return nil, errors.New("interface access denied")
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 1 || !strings.Contains(stdout.String(), "Local interface IP: Unknown") || !strings.Contains(stdout.String(), "203.0.113.10") || stderr.Len() == 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestProxyBypassKeepsActionableSafeDiagnostic(t *testing.T) {
	deps := successfulDependencies()
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(context.Context, egress.Family) (egress.Observation, error) {
			return egress.Observation{}, errors.Join(errors.New("private internal cause"), proxy.ErrProxyBypass)
		})
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 1 || !strings.Contains(stderr.String(), "NO_PROXY") || !strings.Contains(stderr.String(), "--proxy") || strings.Contains(stderr.String(), "private internal cause") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestSystemRouteIsExplicitlyDisclosed(t *testing.T) {
	deps := successfulDependencies()
	deps.client = func(string, time.Duration) (*proxy.Client, error) {
		return &proxy.Client{HTTP: &http.Client{}, Source: proxy.SourceSystemRoute}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 0 || !strings.Contains(stderr.String(), "Desktop proxy settings are not read") || strings.Contains(stdout.String(), "Desktop proxy") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestCancellationIsReportedWithoutStartingInspection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	if code := run(ctx, nil, &stdout, &stderr, dependencies{}); code != 130 || stdout.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestCancellationStopsActiveProbes(t *testing.T) {
	deps := successfulDependencies()
	started := make(chan struct{}, 2)
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(ctx context.Context, _ egress.Family) (egress.Observation, error) {
			started <- struct{}{}
			<-ctx.Done()
			return egress.Observation{}, ctx.Err()
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, &stdout, &stderr, deps) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			<-done
			t.Fatal("both probes did not start")
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("code = %d; want 130", code)
		}
	case <-time.After(time.Second):
		t.Fatal("inspection did not stop after cancellation")
	}
}

func TestInterfaceNamesCannotInjectTerminalControls(t *testing.T) {
	deps := successfulDependencies()
	deps.interfaces = func() ([]netinfo.Address, error) {
		return []netinfo.Address{{Interface: "Ethernet\n\x1b[31m", IP: netip.MustParseAddr("10.0.0.2")}}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 0 || strings.Contains(stdout.String(), "\x1b") || strings.Count(stdout.String(), "\n") != 4 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}
