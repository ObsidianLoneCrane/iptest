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

type inspectionTransport func(*http.Request) (*http.Response, error)

func (transport inspectionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestGeolocationUsesObservedIPsAndSharedTransport(t *testing.T) {
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
			if request.URL.Query().Get("fields") != "ip,success,country,region,city" {
				t.Error("lookup did not request the expected minimal fields")
			}
			switch request.URL.Path {
			case "/8.8.8.8":
				payload = map[string]any{"success": true, "ip": "8.8.8.8", "country": "Japan", "region": "Tokyo Metropolis", "city": "Tokyo"}
			case "/2606:4700:4700::1111":
				payload = map[string]any{"success": true, "ip": "2606:4700:4700:0:0:0:0:1111", "country": "Canada", "region": "Quebec", "city": "Montreal"}
			default:
				return nil, errors.New("geolocation was not queried for the observed IP")
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
	var stdout, stderr bytes.Buffer
	code := runPlain(context.Background(), []string{"--family", "both"}, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	for _, expected := range []string{
		"Public egress IP (IPv4): 8.8.8.8",
		"Estimated location (IPv4): Japan / Tokyo Metropolis / Tokyo",
		"Public egress IP (IPv6): 2606:4700:4700::1111",
		"Estimated location (IPv6): Canada / Quebec / Montreal",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("missing %q in %q", expected, stdout.String())
		}
	}
	for _, target := range []string{"api.ipify.org", "api6.ipify.org", "ipwho.is/8.8.8.8", "ipwho.is/2606:4700:4700::1111"} {
		if requests[target] != 1 {
			t.Errorf("requests to %q = %d; want exactly one", target, requests[target])
		}
	}
}

func TestGeolocationPipelinesAreConcurrentAndStayWithTheirFamilies(t *testing.T) {
	deps := successfulDependencies()
	ipv6LookupStarted := make(chan struct{})
	deps.geolocator = func(*http.Client) geolocator {
		return geolocatorFunc(func(ctx context.Context, ip netip.Addr) (geolocation.Location, error) {
			if ip.Is6() {
				close(ipv6LookupStarted)
				return geolocation.Location{Country: "Canada", City: "Montreal"}, nil
			}
			select {
			case <-ipv6LookupStarted:
				return geolocation.Location{Country: "Japan", City: "Tokyo"}, nil
			case <-ctx.Done():
				return geolocation.Location{}, ctx.Err()
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := runPlain(ctx, []string{"--family", "both"}, &stdout, &stderr, deps)
	first := strings.Index(stdout.String(), "Estimated location (IPv4): Japan / Unknown / Tokyo")
	second := strings.Index(stdout.String(), "Estimated location (IPv6): Canada / Unknown / Montreal")
	if code != 0 || stderr.Len() != 0 || first < 0 || second <= first {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestMissingLocationLevelsAreSuccessfulUnknownValues(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		location geolocation.Location
		want     string
	}{
		{"country only", geolocation.Location{Country: "Japan"}, "Japan / Unknown / Unknown"},
		{"all empty", geolocation.Location{}, "Unknown / Unknown / Unknown"},
		{"control characters", geolocation.Location{Country: " Japan\n", Region: "\x1b\t", City: " Tokyo\r"}, "Japan / Unknown / Tokyo"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deps := successfulDependencies()
			deps.geolocator = func(*http.Client) geolocator {
				return geolocatorFunc(func(context.Context, netip.Addr) (geolocation.Location, error) {
					return testCase.location, nil
				})
			}
			var stdout, stderr bytes.Buffer
			code := runPlain(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Estimated location (IPv4): "+testCase.want) || strings.Count(stdout.String(), "\n") != 4+sourceTableLines(sampleSourceReport()) {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestGeolocationFailureRetainsIPAndOtherFamilyLocation(t *testing.T) {
	deps := successfulDependencies()
	deps.geolocator = func(*http.Client) geolocator {
		return geolocatorFunc(func(_ context.Context, ip netip.Addr) (geolocation.Location, error) {
			if ip.Is6() {
				return geolocation.Location{}, errors.New("geolocation service returned HTTP 429")
			}
			return geolocation.Location{Country: "Japan", City: "Tokyo"}, nil
		})
	}
	var stdout, stderr bytes.Buffer
	code := runPlain(context.Background(), []string{"--family", "both"}, &stdout, &stderr, deps)
	if code != 1 || !strings.Contains(stdout.String(), "Public egress IP (IPv6): 2001:db8::10") || !strings.Contains(stdout.String(), "Estimated location (IPv6): Unknown (lookup failed)") || !strings.Contains(stdout.String(), "Estimated location (IPv4): Japan / Unknown / Tokyo") || !strings.Contains(stderr.String(), "IPv6 geolocation lookup failed") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "IP source data (IPv6, proxycheck.io):\n"+reputation.Summary(sampleSourceReport())) {
		t.Fatalf("geolocation failure suppressed source data: %q", stdout.String())
	}
}

func TestEgressFailureDoesNotQueryDependentSources(t *testing.T) {
	deps := successfulDependencies()
	var lookups atomic.Int32
	var reputationLookups atomic.Int32
	deps.detector = func(*http.Client) detector {
		return detectorFunc(func(context.Context, egress.Family) (egress.Observation, error) {
			return egress.Observation{}, errors.New("egress unavailable")
		})
	}
	deps.geolocator = func(*http.Client) geolocator {
		return geolocatorFunc(func(context.Context, netip.Addr) (geolocation.Location, error) {
			lookups.Add(1)
			return geolocation.Location{}, nil
		})
	}
	deps.reputation = func(*http.Client) reputationLookup {
		return reputationFunc(func(context.Context, netip.Addr) (reputation.Report, error) {
			reputationLookups.Add(1)
			return sampleSourceReport(), nil
		})
	}
	var stdout, stderr bytes.Buffer
	code := runPlain(context.Background(), []string{"--family", "both"}, &stdout, &stderr, deps)
	if code != 1 || lookups.Load() != 0 || reputationLookups.Load() != 0 || strings.Count(stdout.String(), "Unknown (egress unavailable)") != 2 || strings.Contains(stdout.String(), "IP source data") || strings.Contains(stdout.String(), "IP reputation") {
		t.Fatalf("code = %d, geolocation lookups = %d, reputation lookups = %d, stdout = %q", code, lookups.Load(), reputationLookups.Load(), stdout.String())
	}
}

func TestGeolocationRespectsDestinationSpecificProxyBypass(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "ipwho.is")
	deps := successfulDependencies()
	deps.client = proxy.NewClient
	deps.geolocator = func(client *http.Client) geolocator { return geolocation.New(client) }
	var stdout, stderr bytes.Buffer
	code := runPlain(context.Background(), []string{"--family", "ipv4"}, &stdout, &stderr, deps)
	if code != 1 || !strings.Contains(stdout.String(), "Public egress IP (IPv4): 203.0.113.10") || !strings.Contains(stdout.String(), "Unknown (lookup failed)") || !strings.Contains(stderr.String(), "NO_PROXY") || !strings.Contains(stderr.String(), "--proxy") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestCancellationDuringGeolocationRetainsObservedIP(t *testing.T) {
	deps := successfulDependencies()
	started := make(chan struct{})
	deps.geolocator = func(*http.Client) geolocator {
		return geolocatorFunc(func(ctx context.Context, _ netip.Addr) (geolocation.Location, error) {
			close(started)
			<-ctx.Done()
			return geolocation.Location{}, ctx.Err()
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runPlain(ctx, []string{"--family", "ipv4"}, &stdout, &stderr, deps) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("geolocation did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 || !strings.Contains(stdout.String(), "Public egress IP (IPv4): 203.0.113.10") || !strings.Contains(stdout.String(), "Unknown (lookup failed)") {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("geolocation did not stop after cancellation")
	}
}
