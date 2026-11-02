package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDetectUsesFamilySpecificEndpoints(t *testing.T) {
	for _, tc := range []struct {
		family Family
		host   string
		ip     string
	}{
		{IPv4, "api.ipify.org", "8.8.8.8"},
		{IPv6, "api6.ipify.org", "2606:4700:4700::1111"},
	} {
		t.Run(string(tc.family), func(t *testing.T) {
			var calls int
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != tc.host {
					t.Errorf("unexpected probe: %s %s", req.Method, req.URL)
				}
				if req.URL.Query().Get("format") != "json" || req.Header.Get("Accept") != "application/json" {
					t.Error("probe must request JSON")
				}
				return jsonResponse(fmt.Sprintf(`{"ip":%q}`, tc.ip)), nil
			})}
			got, err := New(client).Detect(context.Background(), tc.family)
			if err != nil {
				t.Fatal(err)
			}
			if got.Family != tc.family || got.IP != netip.MustParseAddr(tc.ip) || calls != 1 {
				t.Fatalf("unexpected observation or request count: %+v, calls=%d", got, calls)
			}
		})
	}
}

func TestDetectRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name   string
		family Family
		body   string
	}{
		{"empty body", IPv4, ""},
		{"missing IP", IPv4, `{}`},
		{"empty IP", IPv4, `{"ip":""}`},
		{"null object", IPv4, `null`},
		{"null IP", IPv4, `{"ip":null}`},
		{"numeric IP", IPv4, `{"ip":1234}`},
		{"array", IPv4, `[{"ip":"203.0.113.10"}]`},
		{"non JSON", IPv4, "<html>unavailable</html>"},
		{"non IP", IPv4, `{"ip":"example.com"}`},
		{"IP with port", IPv4, `{"ip":"203.0.113.10:443"}`},
		{"IP with whitespace", IPv4, `{"ip":" 203.0.113.10 "}`},
		{"additional JSON object", IPv4, `{"ip":"203.0.113.10"} {"ip":"203.0.113.11"}`},
		{"additional JSON scalar", IPv4, `{"ip":"203.0.113.10"} true`},
		{"trailing garbage", IPv4, `{"ip":"203.0.113.10"} garbage`},
		{"IPv4 private 10", IPv4, `{"ip":"10.0.0.1"}`},
		{"IPv4 private 172", IPv4, `{"ip":"172.16.0.1"}`},
		{"IPv4 private 192", IPv4, `{"ip":"192.168.0.1"}`},
		{"IPv4 loopback", IPv4, `{"ip":"127.0.0.1"}`},
		{"IPv4 link local", IPv4, `{"ip":"169.254.1.1"}`},
		{"IPv4 unspecified", IPv4, `{"ip":"0.0.0.0"}`},
		{"IPv4 multicast", IPv4, `{"ip":"224.0.0.1"}`},
		{"IPv4 broadcast", IPv4, `{"ip":"255.255.255.255"}`},
		{"IPv4 CGNAT", IPv4, `{"ip":"100.64.0.1"}`},
		{"IPv4 benchmark", IPv4, `{"ip":"198.18.0.1"}`},
		{"IPv4 reserved", IPv4, `{"ip":"240.0.0.1"}`},
		{"IPv4 documentation", IPv4, `{"ip":"203.0.113.10"}`},
		{"IPv6 from IPv4 probe", IPv4, `{"ip":"2001:db8::1"}`},
		{"mapped address from IPv4 probe", IPv4, `{"ip":"::ffff:203.0.113.10"}`},
		{"IPv4 from IPv6 probe", IPv6, `{"ip":"203.0.113.10"}`},
		{"mapped address from IPv6 probe", IPv6, `{"ip":"::ffff:203.0.113.10"}`},
		{"IPv6 private", IPv6, `{"ip":"fd00::1"}`},
		{"IPv6 loopback", IPv6, `{"ip":"::1"}`},
		{"IPv6 link local", IPv6, `{"ip":"fe80::1"}`},
		{"IPv6 unspecified", IPv6, `{"ip":"::"}`},
		{"IPv6 documentation", IPv6, `{"ip":"2001:db8::10"}`},
		{"IPv6 discard", IPv6, `{"ip":"100::1"}`},
		{"IPv6 multicast", IPv6, `{"ip":"ff02::1"}`},
		{"IPv6 zone", IPv6, `{"ip":"2001:db8::1%interface"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			detector := newTestDetector(server)
			got, err := detector.Detect(context.Background(), tc.family)
			if err == nil || got != (Observation{}) {
				t.Fatalf("invalid response accepted: observation=%+v, err=%v", got, err)
			}
		})
	}
}

func TestDetectAcceptsTrailingWhitespace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, " \n{\"ip\":\"8.8.8.8\"}\r\n\t")
	}))
	defer server.Close()
	got, err := newTestDetector(server).Detect(context.Background(), IPv4)
	if err != nil || got.IP.String() != "8.8.8.8" {
		t.Fatalf("valid JSON whitespace rejected: observation=%+v, err=%v", got, err)
	}
}

func TestDetectLimitsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ip":"203.0.113.10","padding":"`+strings.Repeat("x", maxResponseBytes)+`"}`)
	}))
	defer server.Close()
	_, err := newTestDetector(server).Detect(context.Background(), IPv4)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected response size error, got %v", err)
	}
}

func TestDetectRejectsHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = io.WriteString(w, "sensitive-provider-response")
				}
			}))
			defer server.Close()
			_, err := newTestDetector(server).Detect(context.Background(), IPv4)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Fatalf("expected HTTP %d error, got %v", status, err)
			}
			if strings.Contains(err.Error(), "sensitive-provider-response") {
				t.Fatalf("response body leaked: %v", err)
			}
		})
	}
}

func TestDetectDoesNotFollowRedirects(t *testing.T) {
	var redirectedCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedCalls.Add(1)
		_, _ = io.WriteString(w, `{"ip":"203.0.113.10"}`)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	var originalRedirectCalls int
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		originalRedirectCalls++
		return nil
	}
	detector := New(client)
	detector.ipv4URL = server.URL
	_, err := detector.Detect(context.Background(), IPv4)
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("expected redirect status error, got %v", err)
	}
	if redirectedCalls.Load() != 0 || originalRedirectCalls != 0 {
		t.Fatal("detector followed a redirect or used the caller's redirect policy")
	}
	if client.CheckRedirect == nil {
		t.Fatal("detector changed the caller's HTTP client")
	}
}

func TestDetectRequiresAClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("nil client caused a network request")
	}))
	defer server.Close()
	detector := New(nil)
	detector.ipv4URL = server.URL
	_, err := detector.Detect(context.Background(), IPv4)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected a configuration error, got %v", err)
	}
	var nilDetector *Detector
	if _, err := nilDetector.Detect(context.Background(), IPv4); err == nil {
		t.Fatal("nil detector must fail")
	}
}

func TestDetectRejectsInvalidFamilyBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	_, err := newTestDetector(server).Detect(context.Background(), Family("ipv4-sensitive-value"))
	if err == nil || strings.Contains(err.Error(), "sensitive-value") || calls.Load() != 0 {
		t.Fatalf("invalid family was not safely rejected: err=%v, calls=%d", err, calls.Load())
	}
}

func TestDetectHonorsCanceledContext(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := newTestDetector(server).Detect(ctx, IPv4)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request ignored cancellation")
	}
}

func TestDetectHonorsTimeouts(t *testing.T) {
	for _, duringBody := range []bool{false, true} {
		t.Run(fmt.Sprintf("during-body=%t", duringBody), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if duringBody {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			client := server.Client()
			client.Timeout = 50 * time.Millisecond
			detector := New(client)
			detector.ipv4URL = server.URL
			_, err := detector.Detect(context.Background(), IPv4)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected timeout, got %v", err)
			}
		})
	}
}

func TestDetectRedactsRequestErrors(t *testing.T) {
	const secret = "private-proxy-password"
	cause := errors.New("proxy http://user:" + secret + "@proxy.example failed")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, cause
	})}
	_, err := New(client).Detect(context.Background(), IPv4)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "proxy.example") {
		t.Fatalf("transport details leaked: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("sanitized transport failure lost its original cause")
	}
}

func TestDetectRedactsInvalidEndpoint(t *testing.T) {
	detector := New(&http.Client{})
	detector.ipv4URL = "http://user:private-endpoint-password@invalid%host"
	_, err := detector.Detect(context.Background(), IPv4)
	if err == nil || strings.Contains(err.Error(), "private-endpoint-password") {
		t.Fatalf("endpoint details leaked: %v", err)
	}
}

func newTestDetector(server *httptest.Server) *Detector {
	detector := New(server.Client())
	detector.ipv4URL = server.URL
	detector.ipv6URL = server.URL
	return detector
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
