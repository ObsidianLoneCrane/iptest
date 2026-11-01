package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/egress"
	"github.com/ObsidianLoneCrane/iptest/internal/geolocation"
	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
	"github.com/ObsidianLoneCrane/iptest/internal/reputation"
)

func TestFullInspectionUsesObservedIPsAndSharedTransport(t *testing.T) {
	var lock sync.Mutex
	requests := make(map[string]int)
	transport := inspectionTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" {
			t.Errorf("expected HTTPS, got %q", request.URL.Scheme)
		}
		lock.Lock()
		requests[request.URL.Host+request.URL.Path]++
		lock.Unlock()
		var payload map[string]any
		switch request.URL.Host {
		case "api.ipify.org":
			payload = map[string]any{"ip": "8.8.8.8"}
		case "api6.ipify.org":
			payload = map[string]any{"ip": "2606:4700:4700::1111"}
		case "ipwho.is":
			ip := strings.TrimPrefix(request.URL.Path, "/")
			switch ip {
			case "8.8.8.8":
				payload = map[string]any{"success": true, "ip": ip, "country": "Japan", "region": "Tokyo Metropolis", "city": "Tokyo"}
			case "2606:4700:4700::1111":
				payload = map[string]any{"success": true, "ip": ip, "country": "Canada", "region": "Quebec", "city": "Montreal"}
			default:
				return nil, errors.New("location lookup used an unobserved IP")
			}
		case "proxycheck.io":
			if request.URL.Query().Get("ver") != "24-June-2026" || request.URL.Query().Get("tag") != "0" || request.URL.Query().Has("key") || request.URL.Query().Has("contact") {
				t.Error("reputation request did not use the expected anonymous query")
			}
			key := strings.TrimPrefix(request.URL.Path, "/v3/")
			if key != "8.8.8.8" && key != "2606:4700:4700::1111" {
				return nil, errors.New("reputation lookup used an unobserved IP")
			}
			// The returned IPv6 key need not use the request's textual form.
			if key == "2606:4700:4700::1111" {
				key = "2606:4700:4700:0:0:0:0:1111"
			}
			payload = map[string]any{
				"status": "ok",
				key: map[string]any{
					"device_estimate": map[string]any{"address": 2, "subnet": 999},
					"detections":      map[string]any{"compromised": false},
					"attack_history":  map[string]any{},
				},
			}
		default:
			return nil, errors.New("unexpected inspection host")
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	httpClient := &http.Client{Transport: transport, Timeout: time.Second}
	deps := successfulDependencies()
	deps.client = func(string, time.Duration) (*proxy.Client, error) {
		return &proxy.Client{HTTP: httpClient, Source: proxy.SourceExplicit}, nil
	}
	deps.detector = func(client *http.Client) detector {
		if client != httpClient {
			t.Error("egress factory received a different client")
		}
		return egress.New(client)
	}
	deps.geolocator = func(client *http.Client) geolocator {
		if client != httpClient {
			t.Error("geolocation factory received a different client")
		}
		return geolocation.New(client)
	}
	deps.reputation = func(client *http.Client) reputationLookup {
		if client != httpClient {
			t.Error("reputation factory received a different client")
		}
		return reputation.New(client)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 || strings.Count(stdout.String(), "\n") != 5+2*sourceTableLines(sampleSourceReport()) {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	summary := reputation.Summary(sampleSourceReport())
	for _, expected := range []string{
		"Public egress IP (IPv4): 8.8.8.8",
		"Estimated location (IPv4): Japan / Tokyo Metropolis / Tokyo",
		"IP source data (IPv4, proxycheck.io):\n" + summary,
		"Public egress IP (IPv6): 2606:4700:4700::1111",
		"Estimated location (IPv6): Canada / Quebec / Montreal",
		"IP source data (IPv6, proxycheck.io):\n" + summary,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("missing %q in %q", expected, stdout.String())
		}
	}
	if strings.Index(stdout.String(), "Public egress IP (IPv4)") >= strings.Index(stdout.String(), "Public egress IP (IPv6)") {
		t.Error("family output order changed")
	}
	for _, target := range []string{"api.ipify.org", "api6.ipify.org", "ipwho.is/8.8.8.8", "ipwho.is/2606:4700:4700::1111", "proxycheck.io/v3/8.8.8.8", "proxycheck.io/v3/2606:4700:4700::1111"} {
		if requests[target] != 1 {
			t.Errorf("requests to %q = %d; want exactly one", target, requests[target])
		}
	}
}

func TestGeolocationAndReputationRunConcurrently(t *testing.T) {
	deps := successfulDependencies()
	locationStarted := make(chan struct{})
	reputationStarted := make(chan struct{})
	deps.geolocator = func(*http.Client) geolocator {
		return geolocatorFunc(func(ctx context.Context, _ netip.Addr) (geolocation.Location, error) {
			close(locationStarted)
			select {
			case <-reputationStarted:
				return geolocation.Location{Country: "Japan", City: "Tokyo"}, nil
			case <-ctx.Done():
				return geolocation.Location{}, ctx.Err()
			}
		})
	}
	deps.reputation = func(*http.Client) reputationLookup {
		return reputationFunc(func(ctx context.Context, _ netip.Addr) (reputation.Report, error) {
			close(reputationStarted)
			select {
			case <-locationStarted:
				return sampleSourceReport(), nil
			case <-ctx.Done():
				return reputation.Report{}, ctx.Err()
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Japan / Unknown / Tokyo") {
		t.Fatalf("lookups did not finish independently: code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestReputationFailurePreservesIPAndLocation(t *testing.T) {
	secret := "https://private-user:private-password@provider.invalid/"
	for _, testCase := range []struct {
		name string
		err  error
		want string
	}{
		{"denied", errors.Join(reputation.ErrDenied, errors.New(secret)), "anonymous access denied"},
		{"rate limited", errors.Join(reputation.ErrRateLimited, errors.New(secret)), "rate limited"},
		{"timeout", errors.Join(context.DeadlineExceeded, errors.New(secret)), "request timed out"},
		{"lookup failed", errors.New(secret), "request failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deps := successfulDependencies()
			deps.reputation = func(*http.Client) reputationLookup {
				return reputationFunc(func(context.Context, netip.Addr) (reputation.Report, error) {
					return reputation.Report{}, testCase.err
				})
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
			if code != 1 || !strings.Contains(stdout.String(), "Public egress IP (IPv4): 203.0.113.10") || !strings.Contains(stdout.String(), "Estimated location (IPv4): Japan / Tokyo Metropolis / Tokyo") || strings.Contains(stdout.String(), "IP source data") || strings.Contains(stdout.String(), "IP reputation") || !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), "private-password") || strings.Contains(stdout.String()+stderr.String(), "private-user") {
				t.Fatalf("provider or transport details leaked: stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestMissingSourceFieldsAreOmittedWithoutFailing(t *testing.T) {
	zero := int64(0)
	compromised := true
	for _, testCase := range []struct {
		name   string
		report reputation.Report
		lines  int
	}{
		{"missing report fields", reputation.Report{}, 3},
		{"zero estimate and empty history", reputation.Report{EstimatedDevices: &zero, AttackHistory: map[string]int64{}}, 3},
		{"one known detection", reputation.Report{Detections: reputation.Detections{Compromised: &compromised}}, 7},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deps := successfulDependencies()
			deps.reputation = func(*http.Client) reputationLookup {
				return reputationFunc(func(context.Context, netip.Addr) (reputation.Report, error) { return testCase.report, nil })
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Japan / Tokyo Metropolis / Tokyo") || strings.Count(stdout.String(), "\n") != testCase.lines || strings.Contains(stdout.String(), "Unknown") {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if summary := reputation.Summary(testCase.report); summary != "" {
				if !strings.Contains(stdout.String(), "IP source data (IPv4, proxycheck.io):\n"+summary) {
					t.Fatalf("available source facts were omitted: %q", stdout.String())
				}
			} else if strings.Contains(stdout.String(), "IP source data") {
				t.Fatalf("empty source data produced a placeholder line: %q", stdout.String())
			}
		})
	}
}

func TestKnownSourceFactsWithoutAttackHistoryAreDisplayed(t *testing.T) {
	devices := int64(0)
	compromised, hosting := false, true
	report := reputation.Report{
		EstimatedDevices: &devices,
		NetworkType:      "Hosting",
		Detections:       reputation.Detections{Compromised: &compromised, Hosting: &hosting},
	}
	deps := successfulDependencies()
	var requests atomic.Int32
	httpClient := &http.Client{Transport: inspectionTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if request.URL.Host != "proxycheck.io" || request.URL.Path != "/v3/203.0.113.10" {
			t.Errorf("source lookup did not target the observed IP: %s", request.URL)
		}
		body := `{"status":"ok","203.0.113.10":{"network":{"type":"Hosting"},"device_estimate":{"address":0},"detections":{"compromised":false,"hosting":true},"attack_history":null}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	deps.client = func(string, time.Duration) (*proxy.Client, error) {
		return &proxy.Client{HTTP: httpClient, Source: proxy.SourceExplicit}, nil
	}
	deps.reputation = func(client *http.Client) reputationLookup { return reputation.New(client) }
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 || requests.Load() != 1 || strings.Count(stdout.String(), "\n") != 3+sourceTableLines(report) || !strings.Contains(stdout.String(), "IP source data (IPv4, proxycheck.io):\n"+reputation.Summary(report)) || strings.Contains(stdout.String(), "Unknown") {
		t.Fatalf("known facts were lost because history was missing: code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestSourceFactsDoNotProduceOverallJudgment(t *testing.T) {
	compromised := true
	devices := int64(100)
	for _, testCase := range []struct {
		name   string
		report reputation.Report
	}{
		{"compromised flag", reputation.Report{Detections: reputation.Detections{Compromised: &compromised}}},
		{"device estimate", reputation.Report{EstimatedDevices: &devices}},
		{"event counts", reputation.Report{AttackHistory: map[string]int64{"comment_spam": 1, "login_attempt": 99}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deps := successfulDependencies()
			deps.reputation = func(*http.Client) reputationLookup {
				return reputationFunc(func(context.Context, netip.Addr) (reputation.Report, error) { return testCase.report, nil })
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "IP source data (IPv4, proxycheck.io):\n"+reputation.Summary(testCase.report)) {
				t.Fatalf("source facts were not rendered: code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			for _, judgment := range []string{"Risk detected", "Caution", "No known risk", "Unknown", "high estimated sharing", "IP reputation"} {
				if strings.Contains(stdout.String(), judgment) {
					t.Errorf("source facts produced an overall judgment %q: %q", judgment, stdout.String())
				}
			}
		})
	}
}

func TestUsableSourceWarningKeepsSourceFacts(t *testing.T) {
	for _, withFacts := range []bool{false, true} {
		name := "without facts"
		report := reputation.Report{Warning: true}
		lines := 3
		if withFacts {
			name = "with facts"
			report = sampleSourceReport()
			report.Warning = true
			lines = 3 + sourceTableLines(report)
		}
		t.Run(name, func(t *testing.T) {
			deps := successfulDependencies()
			deps.reputation = func(*http.Client) reputationLookup {
				return reputationFunc(func(context.Context, netip.Addr) (reputation.Report, error) { return report, nil })
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
			if code != 0 || !strings.Contains(stderr.String(), "proxycheck.io reported a warning") || strings.Count(stdout.String(), "\n") != lines {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if withFacts && !strings.Contains(stdout.String(), "IP source data (IPv4, proxycheck.io):\n"+reputation.Summary(report)) {
				t.Fatalf("provider warning suppressed valid source facts: %q", stdout.String())
			}
			if !withFacts && strings.Contains(stdout.String(), "IP source data") {
				t.Fatalf("provider warning produced an empty source line: %q", stdout.String())
			}
		})
	}
}

func TestReputationFailureIsIndependentForEachFamily(t *testing.T) {
	deps := successfulDependencies()
	var lookups atomic.Int32
	deps.reputation = func(*http.Client) reputationLookup {
		return reputationFunc(func(_ context.Context, ip netip.Addr) (reputation.Report, error) {
			lookups.Add(1)
			if ip.Is6() {
				return reputation.Report{}, reputation.ErrRateLimited
			}
			return sampleSourceReport(), nil
		})
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, &stdout, &stderr, deps)
	if code != 1 || lookups.Load() != 2 || !strings.Contains(stdout.String(), "IP source data (IPv4, proxycheck.io):\n"+reputation.Summary(sampleSourceReport())) || strings.Contains(stdout.String(), "IP source data (IPv6") || !strings.Contains(stdout.String(), "Estimated location (IPv6): Canada / Quebec / Montreal") || !strings.Contains(stderr.String(), "IPv6 reputation lookup failed: rate limited") {
		t.Fatalf("code = %d, lookups = %d, stdout = %q, stderr = %q", code, lookups.Load(), stdout.String(), stderr.String())
	}
}

func TestReputationRespectsDestinationSpecificProxyBypass(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "proxycheck.io")
	deps := successfulDependencies()
	deps.client = proxy.NewClient
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(context.Context, egress.Family) (egress.Observation, error) {
			return egress.Observation{Family: egress.IPv4, IP: netip.MustParseAddr("8.8.8.8")}, nil
		})
	}
	deps.reputation = func(client *http.Client) reputationLookup { return reputation.New(client) }
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 1 || !strings.Contains(stdout.String(), "Public egress IP (IPv4): 8.8.8.8") || strings.Contains(stdout.String(), "IP source data") || !strings.Contains(stderr.String(), "NO_PROXY") || !strings.Contains(stderr.String(), "--proxy") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestCancellationDuringReputationRetainsOtherResults(t *testing.T) {
	deps := successfulDependencies()
	started := make(chan struct{})
	deps.reputation = func(*http.Client) reputationLookup {
		return reputationFunc(func(ctx context.Context, _ netip.Addr) (reputation.Report, error) {
			close(started)
			<-ctx.Done()
			return reputation.Report{}, ctx.Err()
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"--family", "ipv4"}, &stdout, &stderr, deps) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("reputation lookup did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 || !strings.Contains(stdout.String(), "Public egress IP (IPv4): 203.0.113.10") || !strings.Contains(stdout.String(), "Japan / Tokyo Metropolis / Tokyo") || strings.Contains(stdout.String(), "IP source data") || !strings.Contains(stderr.String(), "request canceled") {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("reputation lookup did not stop after cancellation")
	}
}

func sourceTableLines(report reputation.Report) int {
	table := reputation.Summary(report)
	if table == "" {
		return 0
	}
	return strings.Count(table, "\n") + 2
}
