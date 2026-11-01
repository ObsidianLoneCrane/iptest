package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/netinfo"
	"github.com/ObsidianLoneCrane/iptest/internal/reputation"
	"github.com/ObsidianLoneCrane/iptest/internal/speedtest"
	"github.com/ObsidianLoneCrane/iptest/internal/terminal"
	"github.com/ObsidianLoneCrane/iptest/internal/traffic"
)

func reportDependencies() dependencies {
	deps := successfulDependencies()
	deps.interfaces = func() ([]netinfo.Address, error) {
		return []netinfo.Address{
			{Interface: "Ethernet", IP: netip.MustParseAddr("192.168.1.20")},
			{Interface: "Ethernet", IP: netip.MustParseAddr("fd00::123")},
		}, nil
	}
	deps.traffic = func(context.Context, time.Duration) ([]traffic.Rate, error) {
		return []traffic.Rate{{Interface: "Ethernet", ReceiveBytesPerSecond: 2048, SendBytesPerSecond: 1024}}, nil
	}
	deps.speed = func(*http.Client) speedMeter {
		return speedFunc(func(context.Context, bool) (speedtest.Report, error) {
			return speedtest.Report{Attempts: []speedtest.Attempt{{Result: speedtest.Result{
				Target: "OVHcloud Singapore", DownloadMbps: 16.78, Latency: 85 * time.Millisecond,
				LatencyMeasured: true, DownloadMeasured: true, Bytes: 2 * 1024 * 1024, Duration: time.Second,
			}}}, Bytes: 2 * 1024 * 1024, Duration: 2 * time.Second}, nil
		})
	}
	return deps
}

func TestDefaultReportGroupsResultsAndOnlyInspectsIPv4(t *testing.T) {
	deps := reportDependencies()
	var out, diag bytes.Buffer
	code := run(context.Background(), nil, &out, &diag, deps)
	text := out.String()
	if code != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, diag.String())
	}
	previous := -1
	for _, section := range []string{"iptest / Network inspection", "Connection\n", "Local network\n", "Public egress / IPv4", "IP source data / IPv4 / proxycheck.io", "Network speed / automatic failover"} {
		index := strings.Index(text, section)
		if index <= previous {
			t.Fatalf("missing or unordered section %q: %s", section, text)
		}
		previous = index
	}
	for _, value := range []string{"192.168.1.20", "203.0.113.10", "Japan / Tokyo Metropolis / Tokyo", "Field", "Status", "Meaning", "OVHcloud Singapore", "16.78", "85.0", "2.00 MiB in 1.00 s", "Receive (KiB/s)"} {
		if !strings.Contains(text, value) {
			t.Errorf("report lost %q: %s", value, text)
		}
	}
	if strings.Contains(text, "IPv6") || strings.Contains(text, "fd00::123") || strings.Contains(text, "iptest:") || strings.ContainsAny(text, "\x1b\r") {
		t.Fatalf("unexpected family, diagnostics, or terminal controls: %s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if terminal.DisplayWidth(line) > 100 {
			t.Fatalf("redirected report too wide: %q", line)
		}
	}
	t.Log("Illustrative fixture report (not live IP or speed data):\n" + text)
}

func TestReportKeepsDualStackSeparateAndDoesNotRepeatInterfaceRates(t *testing.T) {
	deps := reportDependencies()
	var out, diag bytes.Buffer
	code := run(context.Background(), []string{"--family", "both", "--no-speedtest"}, &out, &diag, deps)
	text := out.String()
	if code != 0 || strings.Index(text, "Public egress / IPv6") <= strings.Index(text, "Public egress / IPv4") || !strings.Contains(text, "fd00::123") || strings.Count(text, "2.00") != 1 || !strings.Contains(text, "Canada / Quebec / Montreal") || strings.Contains(text, "Network speed") {
		t.Fatalf("families mixed or rates duplicated: code=%d report=%s diagnostics=%s", code, text, diag.String())
	}
}

func TestReportOmitsEmptySourceSectionAndSkippedMeasurements(t *testing.T) {
	deps := successfulDependencies()
	deps.reputation = func(*http.Client) reputationLookup {
		return reputationFunc(func(context.Context, netip.Addr) (reputation.Report, error) { return reputation.Report{}, nil })
	}
	var out, diag bytes.Buffer
	code := run(context.Background(), []string{"--no-speedtest", "--no-traffic"}, &out, &diag, deps)
	text := out.String()
	if code != 0 || strings.Contains(text, "IP source data") || strings.Contains(text, "Network speed") || strings.Contains(text, "KiB/s") || strings.Contains(text, "Unknown") {
		t.Fatalf("omitted data added clutter: code=%d report=%s diagnostics=%s", code, text, diag.String())
	}
}

func TestReportComparisonShowsFailedPartialAndSkippedTargets(t *testing.T) {
	deps := reportDependencies()
	deps.speed = func(*http.Client) speedMeter {
		return speedFunc(func(_ context.Context, compare bool) (speedtest.Report, error) {
			if !compare {
				t.Error("comparison option not forwarded")
			}
			return speedtest.Report{Attempts: []speedtest.Attempt{
				{Result: speedtest.Result{Target: "first"}, Err: io.EOF},
				{Result: speedtest.Result{Target: "partial", Latency: 12 * time.Millisecond, LatencyMeasured: true}, Err: context.DeadlineExceeded},
				{Result: speedtest.Result{Target: "last"}, Skipped: true, Err: speedtest.ErrBudgetExhausted},
			}}, speedtest.ErrBudgetExhausted
		})
	}
	var out, diag bytes.Buffer
	code := run(context.Background(), []string{"--speedtest", "compare"}, &out, &diag, deps)
	for _, text := range []string{"Network speed / sequential comparison", "first", "Failed", "partial", "12.0", "last", "Skipped", "203.0.113.10", "IP source data / IPv4"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("missing %q: %s", text, out.String())
		}
	}
	if code != 1 || strings.Contains(out.String(), "| 0.00 ") || !strings.Contains(diag.String(), "total measurement budget exhausted") {
		t.Fatalf("invalid partial report: code=%d report=%s diagnostics=%s", code, out.String(), diag.String())
	}
}

type afterMeasurementWriter struct {
	buffer   bytes.Buffer
	finished *bool
	t        *testing.T
}

func (writer *afterMeasurementWriter) Write(data []byte) (int, error) {
	if !*writer.finished {
		writer.t.Error("report printed before final measurement returned")
	}
	return writer.buffer.Write(data)
}

func TestReportFlushesCompletedSectionsAfterCancellation(t *testing.T) {
	deps := reportDependencies()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := false
	deps.speed = func(*http.Client) speedMeter {
		return speedFunc(func(context.Context, bool) (speedtest.Report, error) {
			cancel()
			finished = true
			return speedtest.Report{}, context.Canceled
		})
	}
	out := &afterMeasurementWriter{finished: &finished, t: t}
	var diag bytes.Buffer
	code := run(ctx, nil, out, &diag, deps)
	if code != 130 || !strings.Contains(out.buffer.String(), "203.0.113.10") || !strings.Contains(out.buffer.String(), "IP source data / IPv4") {
		t.Fatalf("cancellation discarded completed report: code=%d report=%s diagnostics=%s", code, out.buffer.String(), diag.String())
	}
}

func TestReportNarrowLayoutAndUntrustedText(t *testing.T) {
	var out, diag bytes.Buffer
	view := newOutput(&out, "report")
	view.width = 40
	view.connection("system route (may include TUN/VPN)")
	view.local([]netinfo.Address{{Interface: "\x1b[31m以太网\x1b[0m", IP: netip.MustParseAddr("2001:db8:1234:5678:abcd:ef01:2345:6789")}}, nil, true, true)
	view.egress("IPv6", "2001:db8:1234:5678:abcd:ef01:2345:6789", "Country / State / City")
	view.source("IPv6", sampleSourceReport())
	view.speed(speedtest.Report{Attempts: []speedtest.Attempt{{Result: speedtest.Result{Target: "unreachable"}, Err: errors.New("secret")}}}, true, &diag)
	view.flush()
	for _, line := range strings.Split(out.String(), "\n") {
		if terminal.DisplayWidth(line) > 40 || strings.ContainsAny(line, "\x1b\r") {
			t.Fatalf("unsafe or overwide report: %q", line)
		}
	}
	if !strings.Contains(out.String(), "以太网") || strings.Contains(out.String()+diag.String(), "secret") {
		t.Fatal("missing interface or unsafe diagnostic")
	}
}
