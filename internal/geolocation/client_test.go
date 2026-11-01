package geolocation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
)

func TestLookupMockUsesExplicitIPAndExistingClient(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ip         string
		responseIP string
	}{
		{"IPv4", "203.0.113.10", "203.0.113.10"},
		{"IPv6", "2001:db8::10", "2001:0DB8:0000:0000:0000:0000:0000:0010"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			transport := &recordingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "ipwho.is" || req.URL.Path != "/"+tc.ip {
					t.Errorf("unexpected lookup request: %s %s", req.Method, req.URL)
				}
				if req.URL.Query().Get("fields") != "ip,success,country,region,city" || len(req.URL.Query()) != 1 || req.Header.Get("Accept") != "application/json" {
					t.Errorf("unexpected lookup fields or Accept header: %s, %v", req.URL.RawQuery, req.Header)
				}
				return jsonResponse(http.StatusOK, fmt.Sprintf(`{"ip":%q,"success":true,"country":"Japan","region":"Tokyo","city":"Tokyo"}`, tc.responseIP)), nil
			}}
			original := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			service := New(original)
			if service.client == original || service.client.Transport != transport || service.client.Timeout != original.Timeout {
				t.Fatal("lookup did not clone the client while preserving transport and timeout")
			}
			got, err := service.Lookup(context.Background(), netip.MustParseAddr(tc.ip))
			if err != nil || got != (Location{Country: "Japan", Region: "Tokyo", City: "Tokyo"}) || calls != 1 {
				t.Fatalf("location=%+v, err=%v, calls=%d", got, err, calls)
			}
		})
	}
}

func TestLookupMockAcceptsMissingLocationLevels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		want   Location
	}{
		{"all missing", "", Location{}},
		{"all null", `,"country":null,"region":null,"city":null`, Location{}},
		{"all empty", `,"country":"","region":"","city":""`, Location{}},
		{"country only", `,"country":"Japan"`, Location{Country: "Japan"}},
		{"mixed missing null empty", `,"country":"Japan","city":null`, Location{Country: "Japan"}},
		{"city only", `,"city":"Tokyo"`, Location{City: "Tokyo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := mockService(`{"ip":"203.0.113.10","success":true` + tc.fields + `}`)
			got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
			if err != nil || got != tc.want {
				t.Fatalf("location=%+v, err=%v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestLookupMockSanitizesLocationText(t *testing.T) {
	service := mockService(`{"ip":"203.0.113.10","success":true,"country":"  Japan\r\n\t ","region":" \u001bTokyo\u0000 ","city":"  東京\n  "}`)
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if err != nil || got != (Location{Country: "Japan", Region: "Tokyo", City: "東京"}) {
		t.Fatalf("location=%+v, err=%v", got, err)
	}
}

func TestLookupMockRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"non JSON", "<html>private-provider-diagnostic</html>"},
		{"array", `[]`},
		{"null", `null`},
		{"missing success", `{"ip":"203.0.113.10","country":"Japan"}`},
		{"null success", `{"ip":"203.0.113.10","success":null}`},
		{"false success", `{"ip":"203.0.113.10","success":false,"message":"private-provider-diagnostic"}`},
		{"wrong success type", `{"ip":"203.0.113.10","success":"true"}`},
		{"missing IP", `{"success":true}`},
		{"empty IP", `{"ip":"","success":true}`},
		{"null IP", `{"ip":null,"success":true}`},
		{"numeric IP", `{"ip":1234,"success":true}`},
		{"malformed IP", `{"ip":"private-provider-diagnostic","success":true}`},
		{"IP mismatch", `{"ip":"203.0.113.11","success":true,"country":"Japan"}`},
		{"wrong family", `{"ip":"2001:db8::10","success":true}`},
		{"mapped IP", `{"ip":"::ffff:203.0.113.10","success":true}`},
		{"zoned IP", `{"ip":"203.0.113.10%scope","success":true}`},
		{"numeric country", `{"ip":"203.0.113.10","success":true,"country":123}`},
		{"boolean region", `{"ip":"203.0.113.10","success":true,"country":"Japan","region":true}`},
		{"array city", `{"ip":"203.0.113.10","success":true,"country":"Japan","city":[]}`},
		{"object city", `{"ip":"203.0.113.10","success":true,"city":{}}`},
		{"multiple JSON objects", `{"ip":"203.0.113.10","success":true}{"ip":"203.0.113.11","success":true}`},
		{"additional JSON scalar", `{"ip":"203.0.113.10","success":true} false`},
		{"trailing garbage", `{"ip":"203.0.113.10","success":true} private-provider-diagnostic`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mockService(tc.body).Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
			if err == nil || got != (Location{}) {
				t.Fatalf("invalid response accepted: location=%+v, err=%v", got, err)
			}
			if strings.Contains(err.Error(), "private-provider-diagnostic") {
				t.Fatalf("response content leaked: %v", err)
			}
		})
	}
}

func TestLookupMockBoundsAndWhitespace(t *testing.T) {
	const valid = `{"ip":"203.0.113.10","success":true,"country":"Japan"}`
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"whitespace", "\n\t " + valid + "\r\n ", true},
		{"exact limit", valid + strings.Repeat(" ", maxResponseBytes-len(valid)), true},
		{"over limit", valid + strings.Repeat(" ", maxResponseBytes-len(valid)+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mockService(tc.body).Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
			if tc.ok && (err != nil || got.Country != "Japan") {
				t.Fatalf("valid response rejected: location=%+v, err=%v", got, err)
			}
			if !tc.ok && (err == nil || got != (Location{})) {
				t.Fatalf("oversized response accepted: location=%+v, err=%v", got, err)
			}
		})
	}
}

func TestLookupMockRejectsHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			service := New(&http.Client{Transport: &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonResponse(status, "private-provider-diagnostic"), nil
			}}})
			got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
			if err == nil || got != (Location{}) || !strings.Contains(err.Error(), fmt.Sprint(status)) || strings.Contains(err.Error(), "private-provider-diagnostic") {
				t.Fatalf("location=%+v, err=%v", got, err)
			}
		})
	}
}

func TestLookupMockInvalidInputsDoNotRequest(t *testing.T) {
	var calls int
	service := New(&http.Client{Transport: &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusOK, `{"ip":"203.0.113.10","success":true}`), nil
	}}})
	for _, ip := range []netip.Addr{{}, netip.MustParseAddr("::ffff:203.0.113.10"), netip.MustParseAddr("2001:db8::10%scope")} {
		if got, err := service.Lookup(context.Background(), ip); err == nil || got != (Location{}) {
			t.Fatalf("invalid input accepted: location=%+v, err=%v", got, err)
		}
	}
	if got, err := service.Lookup(nil, netip.MustParseAddr("203.0.113.10")); err == nil || got != (Location{}) {
		t.Fatalf("nil context accepted: location=%+v, err=%v", got, err)
	}
	if calls != 0 {
		t.Fatalf("invalid inputs caused %d requests", calls)
	}
}

func TestLookupMockRequiresClient(t *testing.T) {
	service := New(nil)
	service.endpoint = "unconfigured://lookup-service"
	if service.client != nil {
		t.Fatal("nil client must remain unconfigured")
	}
	for _, c := range []*Client{service, nil} {
		got, err := c.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
		if err == nil || got != (Location{}) || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("location=%+v, err=%v", got, err)
		}
	}
}

func TestLookupMockPreservesProxyFailureWithoutLeakingCredentials(t *testing.T) {
	cause := fmt.Errorf("http://user:private-proxy-password@proxy.example: %w", proxy.ErrProxyBypass)
	service := New(&http.Client{Transport: &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return nil, cause
	}}})
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if got != (Location{}) || !errors.Is(err, proxy.ErrProxyBypass) || !errors.Is(err, cause) {
		t.Fatalf("proxy failure cause lost: location=%+v, err=%v", got, err)
	}
	for _, secret := range []string{"private-proxy-password", "proxy.example", "ipwho.is", "203.0.113.10"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("request detail leaked: %v", err)
		}
	}
}

func TestLookupMockUsesProvidedProxyPolicy(t *testing.T) {
	var policyCalls int
	transport := &http.Transport{Proxy: func(req *http.Request) (*url.URL, error) {
		policyCalls++
		if req.URL.Host != "ipwho.is" {
			t.Errorf("proxy policy saw an unexpected target: %s", req.URL.Host)
		}
		return nil, proxy.ErrProxyBypass
	}}
	defer transport.CloseIdleConnections()
	service := New(&http.Client{Transport: transport})
	_, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if !errors.Is(err, proxy.ErrProxyBypass) || policyCalls != 1 {
		t.Fatalf("proxy policy bypassed: err=%v, calls=%d", err, policyCalls)
	}
}

func TestLookupMockCancellationBeforeRequest(t *testing.T) {
	var calls int
	service := New(&http.Client{Transport: &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	}}})
	for _, timeout := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := context.Canceled
		if timeout {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		got, err := service.Lookup(ctx, netip.MustParseAddr("203.0.113.10"))
		cancel()
		if got != (Location{}) || !errors.Is(err, want) {
			t.Fatalf("cancellation cause lost: location=%+v, err=%v", got, err)
		}
	}
	if calls != 0 {
		t.Fatalf("canceled lookup made %d requests", calls)
	}
}

func TestLookupMockHonorsClientTimeout(t *testing.T) {
	service := New(&http.Client{
		Timeout: 20 * time.Millisecond,
		Transport: &recordingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}},
	})
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if got != (Location{}) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout not recognized: location=%+v, err=%v", got, err)
	}
}

func TestLookupMockTimeoutWhileReadingBody(t *testing.T) {
	var started atomic.Bool
	service := New(&http.Client{
		Timeout: 50 * time.Millisecond,
		Transport: &recordingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: &deadlineBody{ctx: req.Context(), started: &started}}, nil
		}},
	})
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if !started.Load() || got != (Location{}) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body timeout not recognized: started=%v, location=%+v, err=%v", started.Load(), got, err)
	}
}

type deadlineBody struct {
	ctx     context.Context
	started *atomic.Bool
}

func (body *deadlineBody) Read([]byte) (int, error) {
	body.started.Store(true)
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*deadlineBody) Close() error { return nil }

func TestLookupMockRedactsBodyReadFailures(t *testing.T) {
	cause := errors.New("private-read-error")
	service := New(&http.Client{Transport: &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: failingBody{cause}}, nil
	}}})
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if got != (Location{}) || !errors.Is(err, cause) || strings.Contains(err.Error(), "private-read-error") {
		t.Fatalf("body error cause lost or leaked: location=%+v, err=%v", got, err)
	}
}

func TestLookupMockRedactsInvalidEndpoint(t *testing.T) {
	service := New(&http.Client{})
	service.endpoint = "https://user:private-endpoint-password@invalid%host"
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if err == nil || got != (Location{}) || strings.Contains(err.Error(), "private-endpoint-password") {
		t.Fatalf("endpoint error leaked: location=%+v, err=%v", got, err)
	}
}

func TestLookupLoopbackUsesFixedIPv6Path(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/2001:db8::10" || req.URL.Query().Get("fields") != "ip,success,country,region,city" {
			t.Errorf("lookup did not use the frozen IP: %s", req.URL)
		}
		_, _ = io.WriteString(w, `{"ip":"2001:db8::10","success":true,"country":"Japan"}`)
	}))
	defer server.Close()
	service := New(server.Client())
	service.endpoint = server.URL
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("2001:db8::10"))
	if err != nil || got.Country != "Japan" {
		t.Fatalf("location=%+v, err=%v", got, err)
	}
}

func TestLookupLoopbackDoesNotFollowRedirects(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		_, _ = io.WriteString(w, `{"ip":"203.0.113.10","success":true}`)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, target.URL, http.StatusFound)
	}))
	defer server.Close()
	var originalRedirectCalls int
	original := server.Client()
	original.CheckRedirect = func(*http.Request, []*http.Request) error {
		originalRedirectCalls++
		return nil
	}
	service := New(original)
	service.endpoint = server.URL
	got, err := service.Lookup(context.Background(), netip.MustParseAddr("203.0.113.10"))
	if err == nil || got != (Location{}) || !strings.Contains(err.Error(), "302") || targetCalls.Load() != 0 || originalRedirectCalls != 0 {
		t.Fatalf("redirect followed: location=%+v, err=%v, calls=%d", got, err, targetCalls.Load())
	}
}

func mockService(body string) *Client {
	return New(&http.Client{Transport: &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, body), nil
	}}})
}

type recordingTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (transport *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport.roundTrip(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

type failingBody struct{ err error }

func (body failingBody) Read([]byte) (int, error) { return 0, body.err }

func (body failingBody) Close() error { return nil }
