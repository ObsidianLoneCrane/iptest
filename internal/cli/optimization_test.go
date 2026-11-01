package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/egress"
	"github.com/ObsidianLoneCrane/iptest/internal/netinfo"
	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
	"github.com/ObsidianLoneCrane/iptest/internal/reputation"
	"github.com/ObsidianLoneCrane/iptest/internal/speedtest"
	"github.com/ObsidianLoneCrane/iptest/internal/traffic"
)

type speedFunc func(context.Context, bool) (speedtest.Report, error)

func (f speedFunc) Run(ctx context.Context, compare bool) (speedtest.Report, error) {
	return f(ctx, compare)
}

func TestTrafficPrecedesProbesAndSpeedFollowsLookups(t *testing.T) {
	deps := successfulDependencies()
	client := &http.Client{}
	deps.client = func(string, time.Duration) (*proxy.Client, error) {
		return &proxy.Client{HTTP: client, Source: proxy.SourceSystemProxy, Detail: "test system proxy"}, nil
	}
	var sampled atomic.Bool
	var lookedUp atomic.Bool
	deps.traffic = func(context.Context, time.Duration) ([]traffic.Rate, error) {
		sampled.Store(true)
		return []traffic.Rate{{Interface: "Ethernet", ReceiveBytesPerSecond: 2048, SendBytesPerSecond: 1024}}, nil
	}
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(context.Context, egress.Family) (egress.Observation, error) {
			if !sampled.Load() {
				t.Error("probe started before passive sampling finished")
			}
			return egress.Observation{Family: egress.IPv4, IP: netip.MustParseAddr("8.8.8.8")}, nil
		})
	}
	originalLookup := deps.reputation
	deps.reputation = func(c *http.Client) reputationLookup {
		original := originalLookup(c)
		return reputationFunc(func(ctx context.Context, ip netip.Addr) (report reputation.Report, err error) {
			report, err = original.Lookup(ctx, ip)
			lookedUp.Store(true)
			return
		})
	}
	deps.speed = func(c *http.Client) speedMeter {
		if c != client {
			t.Error("speed test received another route")
		}
		return speedFunc(func(_ context.Context, compare bool) (speedtest.Report, error) {
			if compare {
				t.Error("default speed mode must be automatic")
			}
			if !lookedUp.Load() {
				t.Error("speed test overlapped source lookups")
			}
			return speedtest.Report{Attempts: []speedtest.Attempt{{Result: speedtest.Result{Target: "fixture", DownloadMbps: 12.5, Latency: 25 * time.Millisecond, Bytes: 1000, Duration: time.Second, LatencyMeasured: true, DownloadMeasured: true}}}}, nil
		})
	}
	var out, diag bytes.Buffer
	code := runPlain(context.Background(), []string{"--family", "ipv4"}, &out, &diag, deps)
	for _, part := range []string{"Network path: test system proxy", "receive 2.00 KiB/s; send 1.00 KiB/s", "download 12.50 Mbps; HTTP response latency 25.0 ms"} {
		if !strings.Contains(out.String(), part) {
			t.Errorf("missing %q in %q", part, out.String())
		}
	}
	if code != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, diag.String())
	}
}

func TestMetricOptionsSkipMeasurement(t *testing.T) {
	deps := successfulDependencies()
	deps.traffic = func(context.Context, time.Duration) ([]traffic.Rate, error) {
		t.Error("traffic must be skipped")
		return nil, nil
	}
	deps.speed = func(*http.Client) speedMeter { t.Error("speed must be skipped"); return nil }
	var out, diag bytes.Buffer
	if code := runPlain(context.Background(), []string{"--no-speedtest", "--no-traffic", "--family", "ipv4"}, &out, &diag, deps); code != 0 {
		t.Fatalf("code=%d", code)
	}
}

func TestAutoFamilyRetainsAvailableExitWithoutUnknown(t *testing.T) {
	deps := successfulDependencies()
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(_ context.Context, family egress.Family) (egress.Observation, error) {
			if family == egress.IPv6 {
				return egress.Observation{}, errors.New("probe unavailable")
			}
			return egress.Observation{Family: family, IP: netip.MustParseAddr("8.8.8.8")}, nil
		})
	}
	var out, diag bytes.Buffer
	if code := runPlain(context.Background(), []string{"--family", "auto"}, &out, &diag, deps); code != 0 || strings.Contains(out.String(), "Unknown") || !strings.Contains(diag.String(), "another family is available") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
}

func TestSpeedFailurePreservesIPAndSourceData(t *testing.T) {
	deps := successfulDependencies()
	deps.speed = func(*http.Client) speedMeter {
		return speedFunc(func(context.Context, bool) (speedtest.Report, error) {
			return speedtest.Report{}, context.DeadlineExceeded
		})
	}
	var out, diag bytes.Buffer
	code := runPlain(context.Background(), []string{"--family", "ipv4"}, &out, &diag, deps)
	if code != 1 || !strings.Contains(out.String(), "Public egress IP (IPv4):") || !strings.Contains(out.String(), "IP source data") || !strings.Contains(diag.String(), "request timed out") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
}

func TestCanceledTrafficSampleDoesNotStartNetworkRequests(t *testing.T) {
	deps := successfulDependencies()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps.traffic = func(context.Context, time.Duration) ([]traffic.Rate, error) { cancel(); return nil, context.Canceled }
	deps.detector = func(*http.Client) detector { t.Error("probe factory called after cancellation"); return nil }
	var out, diag bytes.Buffer
	if code := runPlain(ctx, nil, &out, &diag, deps); code != 130 {
		t.Fatalf("code=%d", code)
	}
}

func TestFailedEgressSkipsActiveMeasurement(t *testing.T) {
	deps := successfulDependencies()
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(context.Context, egress.Family) (egress.Observation, error) {
			return egress.Observation{}, errors.New("unavailable")
		})
	}
	deps.speed = func(*http.Client) speedMeter { t.Error("active measurement ran without a usable egress"); return nil }
	var out, diag bytes.Buffer
	if code := runPlain(context.Background(), nil, &out, &diag, deps); code != 1 {
		t.Fatalf("code=%d", code)
	}
}

func TestUnsupportedAutomaticProxyIsExplainedWithoutLeakingConfiguration(t *testing.T) {
	deps := successfulDependencies()
	deps.client = func(string, time.Duration) (*proxy.Client, error) {
		return nil, errors.Join(proxy.ErrAutoProxyUnsupported, errors.New("https://user:secret@private.invalid/proxy.pac"))
	}
	var out, diag bytes.Buffer
	code := runPlain(context.Background(), nil, &out, &diag, deps)
	if code != 2 || out.Len() != 0 || !strings.Contains(diag.String(), "configuration is enabled") || strings.Contains(diag.String(), "secret") || strings.Contains(diag.String(), "private.invalid") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
}

func TestAddressFamilyControlsProbesAndLocalOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		args   []string
		v4, v6 bool
	}{
		{name: "default IPv4", v4: true},
		{name: "explicit IPv4", args: []string{"--family", "ipv4"}, v4: true},
		{name: "IPv6 only", args: []string{"--family", "ipv6"}, v6: true},
		{name: "add IPv6", args: []string{"--family", "both"}, v4: true, v6: true},
		{name: "available families", args: []string{"--family", "auto"}, v4: true, v6: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := successfulDependencies()
			deps.interfaces = func() ([]netinfo.Address, error) {
				return []netinfo.Address{
					{Interface: "Ethernet", IP: netip.MustParseAddr("192.168.1.20")},
					{Interface: "Ethernet", IP: netip.MustParseAddr("fd00::123")},
				}, nil
			}
			var called4, called6 atomic.Int32
			originalDetector := deps.detector
			deps.detector = func(client *http.Client) detector {
				original := originalDetector(client)
				return detectorFunc(func(ctx context.Context, family egress.Family) (egress.Observation, error) {
					if family == egress.IPv4 {
						called4.Add(1)
					} else {
						called6.Add(1)
					}
					return original.Detect(ctx, family)
				})
			}
			var out, diag bytes.Buffer
			code := runPlain(context.Background(), test.args, &out, &diag, deps)
			if code != 0 || (called4.Load() == 1) != test.v4 || (called6.Load() == 1) != test.v6 || strings.Contains(out.String(), "192.168.1.20") != test.v4 || strings.Contains(out.String(), "fd00::123") != test.v6 || strings.Contains(out.String(), "Public egress IP (IPv4)") != test.v4 || strings.Contains(out.String(), "IP source data (IPv6") != test.v6 {
				t.Fatalf("family selection leaked or omitted output: code=%d calls=%d/%d stdout=%s stderr=%s", code, called4.Load(), called6.Load(), out.String(), diag.String())
			}
		})
	}
}

func TestCompareModeListsFailuresAndPreservesPartialMetrics(t *testing.T) {
	deps := successfulDependencies()
	deps.speed = func(*http.Client) speedMeter {
		return speedFunc(func(_ context.Context, compare bool) (speedtest.Report, error) {
			if !compare {
				t.Error("comparison flag not forwarded")
			}
			return speedtest.Report{Attempts: []speedtest.Attempt{
				{Result: speedtest.Result{Target: "first"}, Err: errors.New("secret transport text")},
				{Result: speedtest.Result{Target: "second", Latency: 12 * time.Millisecond, LatencyMeasured: true}, Err: context.DeadlineExceeded},
				{Result: speedtest.Result{Target: "third", Latency: 20 * time.Millisecond, DownloadMbps: 5, LatencyMeasured: true, DownloadMeasured: true}},
			}}, context.DeadlineExceeded
		})
	}
	var out, diag bytes.Buffer
	code := runPlain(context.Background(), []string{"--speedtest", "compare"}, &out, &diag, deps)
	for _, text := range []string{
		"Network speed (first): status Failed",
		"Network speed (second): HTTP response latency 12.0 ms; status Failed",
		"Network speed (third): download 5.00 Mbps; HTTP response latency 20.0 ms; status OK",
		"Public egress IP (IPv4)",
	} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("missing %q: %s", text, out.String())
		}
	}
	if code != 1 || strings.Contains(out.String()+diag.String(), "secret") || strings.Contains(out.String(), "download 0.00") || strings.Contains(out.String(), "IPv6") {
		t.Fatalf("invalid comparison output: code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
}

func TestSuccessfulAutomaticFailoverDoesNotFailInvocation(t *testing.T) {
	deps := successfulDependencies()
	deps.speed = func(*http.Client) speedMeter {
		return speedFunc(func(_ context.Context, compare bool) (speedtest.Report, error) {
			if compare {
				t.Error("unexpected compare mode")
			}
			return speedtest.Report{Attempts: []speedtest.Attempt{
				{Result: speedtest.Result{Target: "unavailable"}, Err: &speedtest.HTTPError{StatusCode: 429}},
				{Result: speedtest.Result{Target: "available", Latency: time.Millisecond, DownloadMbps: 10, LatencyMeasured: true, DownloadMeasured: true}},
			}}, nil
		})
	}
	var out, diag bytes.Buffer
	if code := runPlain(context.Background(), nil, &out, &diag, deps); code != 0 || !strings.Contains(out.String(), "Network speed (available)") || strings.Contains(out.String(), "Network speed (unavailable)") || !strings.Contains(diag.String(), "HTTP 429") {
		t.Fatalf("fallback success lost: code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
}

func TestNoSpeedtestOverridesCompare(t *testing.T) {
	deps := successfulDependencies()
	deps.speed = func(*http.Client) speedMeter { t.Error("comparison ran despite skip option"); return nil }
	var out, diag bytes.Buffer
	if code := runPlain(context.Background(), []string{"--speedtest", "compare", "--no-speedtest"}, &out, &diag, deps); code != 0 || strings.Contains(out.String(), "Network speed") {
		t.Fatalf("skip ignored: code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
}
