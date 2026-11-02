package reputation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
)

const fixtureIP = "203.0.113.10"

func TestLookupUsesFrozenIPAndProvidedTransport(t *testing.T) {
	for _, tc := range []struct {
		name, ip, key string
	}{
		{"IPv4", fixtureIP, fixtureIP},
		{"IPv6", "2001:db8::10", "2001:0DB8:0000:0000:0000:0000:0000:0010"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, redirects int
			transport := reputationTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "proxycheck.io" || req.URL.Path != "/v3/"+tc.ip {
					t.Errorf("unexpected lookup method or frozen IP target: %s %s", req.Method, req.URL)
				}
				if got := req.URL.Query(); len(got) != 2 || got.Get("ver") != "24-June-2026" || got.Get("tag") != "0" {
					t.Errorf("unexpected lookup parameters: %v", got)
				}
				if req.Header.Get("Accept") != "application/json" || req.Header.Get("Authorization") != "" || req.Header.Get("Key") != "" {
					t.Errorf("unexpected lookup authentication or Accept header")
				}
				body := fmt.Sprintf(`{"status":"ok",%q:{"network":{"type":"Hosting"},"device_estimate":{"address":2,"subnet":999},"detections":{"compromised":false,"proxy":false,"vpn":true,"tor":false,"hosting":true,"scraper":false},"attack_history":{}}}`, tc.key)
				return reputationResponse(http.StatusOK, body), nil
			})
			original := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
				redirects++
				return nil
			}}
			got, err := New(original).Lookup(context.Background(), netip.MustParseAddr(tc.ip))
			if err != nil || calls != 1 || redirects != 0 {
				t.Fatalf("lookup error=%v, requests=%d, redirects=%d", err, calls, redirects)
			}
			if got.EstimatedDevices == nil || *got.EstimatedDevices != 2 || got.NetworkType != "Hosting" || got.Warning || got.AttackHistory == nil || len(got.AttackHistory) != 0 {
				t.Fatalf("unexpected report: %+v", got)
			}
			if got.Detections.Compromised == nil || *got.Detections.Compromised || got.Detections.VPN == nil || !*got.Detections.VPN || got.Detections.Hosting == nil || !*got.Detections.Hosting {
				t.Fatalf("detection booleans not preserved: %+v", got.Detections)
			}
			// Construction must leave the caller's redirect policy unchanged.
			if err := original.CheckRedirect(nil, nil); err != nil || redirects != 1 || original.Timeout != time.Second {
				t.Fatal("lookup modified the caller's client")
			}
		})
	}
}

func TestLookupPreservesUnknownAndKnownEmptyFields(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		knownEmpty   bool
	}{
		{"missing", `{}`, false},
		{"null sections", `{"network":null,"device_estimate":null,"detections":null,"attack_history":null}`, false},
		{"null consumed fields", `{"network":{"type":null},"device_estimate":{"address":null},"detections":{"compromised":null,"proxy":null,"vpn":null,"tor":null,"hosting":null,"scraper":null}}`, false},
		{"empty object history", `{"attack_history":{}}`, true},
		{"empty array history", `{"attack_history":[]}`, true},
		{"unconsumed fields", `{"network":{"range":23,"provider":[]},"device_estimate":{"subnet":"unconsumed"},"detections":{"risk":{"unconsumed":true},"anonymous":"unconsumed"},"operator":false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reputationService(`{"status":"ok","`+fixtureIP+`":`+tc.fields+`}`).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
			if err != nil {
				t.Fatalf("optional fields rejected: %v", err)
			}
			want := Report{}
			if tc.knownEmpty {
				want.AttackHistory = map[string]int64{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("missing fields became facts: got=%+v, want=%+v", got, want)
			}
		})
	}
}

func TestLookupPreservesDeviceAndHistoryCounts(t *testing.T) {
	body := `{"status":"ok","` + fixtureIP + `":{"device_estimate":{"address":0},"attack_history":{"login_attempt":3,"comment_spam":0,"new_provider_type":9223372036854775807}}}`
	got, err := reputationService(body).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
	if err != nil || got.EstimatedDevices == nil || *got.EstimatedDevices != 0 || !reflect.DeepEqual(got.AttackHistory, map[string]int64{"login_attempt": 3, "comment_spam": 0, "new_provider_type": 9223372036854775807}) {
		t.Fatalf("counts not preserved: report=%+v, err=%v", got, err)
	}
}

func TestLookupOmitsInvalidOptionalFields(t *testing.T) {
	for _, fields := range []string{
		`{"network":[]}`, `{"network":{"type":42}}`,
		`{"network":{"type":""}}`, `{"network":{"type":" \t "}}`,
		`{"device_estimate":[]}`, `{"device_estimate":{"address":-1}}`,
		`{"device_estimate":{"address":1.5}}`, `{"device_estimate":{"address":"2"}}`,
		`{"device_estimate":{"address":true}}`, `{"device_estimate":{"address":{}}}`,
		`{"device_estimate":{"address":9223372036854775808}}`,
		`{"device_estimate":{"address":-1,"subnet":50}}`,
		`{"detections":[]}`, `{"detections":{"compromised":"false"}}`,
		`{"detections":{"proxy":1}}`, `{"detections":{"vpn":{}}}`,
		`{"detections":{"tor":[]}}`, `{"detections":{"hosting":"true"}}`,
		`{"detections":{"scraper":0}}`, `{"attack_history":true}`,
		`{"attack_history":[1]}`, `{"attack_history":[{}]}`,
		`{"attack_history":{"comment_spam":-1}}`, `{"attack_history":{"comment_spam":1.5}}`,
		`{"attack_history":{"comment_spam":"2"}}`, `{"attack_history":{"comment_spam":true}}`,
		`{"attack_history":{"comment_spam":null}}`, `{"attack_history":{"comment_spam":[]}}`,
		`{"attack_history":{"comment_spam":9223372036854775808}}`,
	} {
		t.Run(fields, func(t *testing.T) {
			got, err := reputationService(`{"status":"ok","`+fixtureIP+`":`+fields+`}`).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
			if err != nil || !reflect.DeepEqual(got, Report{}) {
				t.Fatalf("invalid optional field should be omitted: report=%+v, err=%v", got, err)
			}
		})
	}
}

func TestLookupRetainsFactsAlongsideInvalidOptionalFields(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         Report
	}{
		{
			name:   "invalid network retains devices and flags",
			fields: `{"network":{"type":42},"device_estimate":{"address":2},"detections":{"compromised":false},"attack_history":{"comment_spam":3}}`,
			want: Report{EstimatedDevices: reputationPointer(int64(2)), Detections: Detections{Compromised: reputationPointer(false)},
				AttackHistory: map[string]int64{"comment_spam": 3}},
		},
		{
			name:   "invalid estimate never falls back to subnet",
			fields: `{"network":{"type":" Hosting "},"device_estimate":{"address":"2","subnet":900},"detections":{"compromised":true}}`,
			want:   Report{NetworkType: "Hosting", Detections: Detections{Compromised: reputationPointer(true)}},
		},
		{
			name:   "flags are independent and false is a fact",
			fields: `{"device_estimate":{"address":0},"detections":{"compromised":"bad","proxy":false,"vpn":true,"tor":0,"hosting":null,"scraper":true}}`,
			want: Report{EstimatedDevices: reputationPointer(int64(0)),
				Detections: Detections{Proxy: reputationPointer(false), VPN: reputationPointer(true), Scraper: reputationPointer(true)}},
		},
		{
			name:   "history retains individual valid counts",
			fields: `{"network":{"type":"Business"},"attack_history":{"comment_spam":3,"login_attempt":0,"negative":-2,"null":null,"fraction":2.5,"string":"1","boolean":false,"overflow":9223372036854775808}}`,
			want:   Report{NetworkType: "Business", AttackHistory: map[string]int64{"comment_spam": 3, "login_attempt": 0}},
		},
		{
			name:   "all invalid counts do not claim empty history",
			fields: `{"network":{"type":"Residential"},"attack_history":{"comment_spam":null,"bad":-1}}`,
			want:   Report{NetworkType: "Residential"},
		},
		{
			name:   "bad history type retains other sections",
			fields: `{"network":{"type":"Hosting"},"device_estimate":{"address":2},"detections":{"vpn":true},"attack_history":[1]}`,
			want:   Report{NetworkType: "Hosting", EstimatedDevices: reputationPointer(int64(2)), Detections: Detections{VPN: reputationPointer(true)}},
		},
		{
			name:   "malformed sections retain valid history",
			fields: `{"network":[],"device_estimate":false,"detections":"invalid","attack_history":{"future_event":2}}`,
			want:   Report{AttackHistory: map[string]int64{"future_event": 2}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reputationService(`{"status":"ok","`+fixtureIP+`":`+tc.fields+`}`).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("valid facts lost: report=%+v, err=%v; want=%+v", got, err, tc.want)
			}
		})
	}
}

func TestLookupRejectsInvalidOrAmbiguousResponse(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `<html>private-provider-diagnostic</html>`,
		`{"` + fixtureIP + `":{}}`,
		`{"status":null,"` + fixtureIP + `":{}}`,
		`{"status":true,"` + fixtureIP + `":{}}`,
		`{"status":"unexpected","` + fixtureIP + `":{}}`,
		`{"status":"error","` + fixtureIP + `":{},"message":"private-provider-diagnostic"}`,
		`{"status":"ok"}`, `{"status":"ok","203.0.113.11":{}}`,
		`{"status":"ok","` + fixtureIP + `":null}`,
		`{"status":"ok","` + fixtureIP + `":[]}`,
		`{"status":"ok","` + fixtureIP + `":false}`,
		`{"status":"ok","` + fixtureIP + `":"not an object"}`,
		`{"status":"ok","2001:db8::10":{}}`,
		`{"status":"ok","::ffff:203.0.113.10":{}}`,
		`{"status":"ok","` + fixtureIP + `":{},"203.0.113.11":{}}`,
		`{"status":"ok","` + fixtureIP + `":{},"2001:db8::10":null}`,
		`{"status":"ok","` + fixtureIP + `":{},"` + fixtureIP + `":{}}`,
		`{"status":"denied","status":"ok","` + fixtureIP + `":{}}`,
		`{"status":"ok","` + fixtureIP + `":{"detections":{"compromised":true,"compromised":false}}}`,
		`{"status":"ok","` + fixtureIP + `":{"detections":{"compromised":true,"compromised":null}}}`,
		`{"status":"ok","` + fixtureIP + `":{"attack_history":{"comment_spam":5,"comment_spam":0}}}`,
		`{"status":"ok","` + fixtureIP + `":{"device_estimate":{"address":50,"address":null}}}`,
		`{"status":"ok","` + fixtureIP + `":{"detections":{"compromised":true,"Compromised":false}}}`,
		`{"status":"ok","` + fixtureIP + `":{"detections":{"compromised":true,"compromi\u017fed":false}}}`,
		`{"status":"ok","` + fixtureIP + `":{"attack_history":{"comment_spam":5},"ATTACK_HISTORY":{}}}`,
		`{"status":"ok","` + fixtureIP + `":{"device_estimate":{"address":200,"Address":2}}}`,
		`{"status":"ok","` + fixtureIP + `":{}} {"status":"ok"}`,
		`{"status":"ok","` + fixtureIP + `":{}} false`,
		`{"status":"ok","` + fixtureIP + `":{}} private-provider-diagnostic`,
	} {
		t.Run(body, func(t *testing.T) {
			assertReputationFailure(t, body)
		})
	}
	for _, body := range []string{
		`{"status":"ok","2001:db8::10":{},"2001:0db8:0:0:0:0:0:10":{}}`,
		`{"status":"ok","2001:db8::11":{}}`,
	} {
		got, err := reputationService(body).Lookup(context.Background(), netip.MustParseAddr("2001:db8::10"))
		if err == nil || !reflect.DeepEqual(got, Report{}) {
			t.Fatalf("ambiguous IPv6 response accepted: report=%+v, err=%v", got, err)
		}
	}
}

func TestLookupHandlesDenialQuotaAndWarning(t *testing.T) {
	for _, tc := range []struct {
		name string
		http int
		body string
		want error
	}{
		{"HTTP unauthorized", 401, "private-provider-diagnostic", ErrDenied},
		{"HTTP forbidden", 403, "private-provider-diagnostic", ErrDenied},
		{"HTTP rate limit", 429, `{"status":"denied"}`, ErrRateLimited},
		{"JSON denial", 200, `{"status":"denied","message":"private-provider-diagnostic"}`, ErrDenied},
		{"redirect", 302, "private-provider-diagnostic", nil},
		{"no content", 204, "", nil},
		{"provider unavailable", 503, "private-provider-diagnostic", nil},
		{"warning without entry", 200, `{"status":"warning","message":"private-provider-diagnostic"}`, nil},
		{"warning wrong IP", 200, `{"status":"warning","203.0.113.11":{}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := New(&http.Client{Transport: reputationTransport(func(*http.Request) (*http.Response, error) {
				return reputationResponse(tc.http, tc.body), nil
			})})
			got, err := service.Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
			if err == nil || !reflect.DeepEqual(got, Report{}) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("report=%+v, err=%v; expected error %v", got, err, tc.want)
			}
			if strings.Contains(err.Error(), "private-provider-diagnostic") {
				t.Fatalf("provider diagnostic leaked: %v", err)
			}
		})
	}
	for _, fields := range []string{`{}`, `{"device_estimate":{"address":2},"attack_history":{}}`, `{"device_estimate":{"address":"bad"},"detections":{"compromised":true}}`} {
		got, err := reputationService(`{"status":"warning","message":"private-provider-diagnostic","`+fixtureIP+`":`+fields+`}`).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
		if err != nil || !got.Warning {
			t.Fatalf("valid warning response rejected: report=%+v, err=%v", got, err)
		}
	}
}

func TestLookupBoundsResponseAndAllowsWhitespace(t *testing.T) {
	valid := `{"status":"ok","` + fixtureIP + `":{}}`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"whitespace", "\n\t" + valid + "\r\n ", true},
		{"exact 128 KiB", valid + strings.Repeat(" ", 128*1024-len(valid)), true},
		{"over 128 KiB", valid + strings.Repeat(" ", 128*1024-len(valid)+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reputationService(tc.body).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
			if tc.valid && err != nil {
				t.Fatalf("valid response rejected: %v", err)
			}
			if !tc.valid && (err == nil || !reflect.DeepEqual(got, Report{})) {
				t.Fatalf("oversized response accepted: report=%+v, err=%v", got, err)
			}
		})
	}
}

func TestLookupRejectsInvalidInputsWithoutRequesting(t *testing.T) {
	var calls int
	service := New(&http.Client{Transport: reputationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return reputationResponse(http.StatusOK, `{"status":"ok"}`), nil
	})})
	for _, address := range []netip.Addr{{}, netip.MustParseAddr("::ffff:203.0.113.10"), netip.MustParseAddr("2001:db8::10%scope")} {
		if got, err := service.Lookup(context.Background(), address); err == nil || !reflect.DeepEqual(got, Report{}) {
			t.Fatalf("invalid IP accepted: report=%+v, err=%v", got, err)
		}
	}
	if got, err := service.Lookup(nil, netip.MustParseAddr(fixtureIP)); err == nil || !reflect.DeepEqual(got, Report{}) {
		t.Fatalf("nil context accepted: report=%+v, err=%v", got, err)
	}
	for _, unconfigured := range []*Client{nil, New(nil)} {
		got, err := unconfigured.Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
		if err == nil || !reflect.DeepEqual(got, Report{}) {
			t.Fatalf("missing client enabled fallback: report=%+v, err=%v", got, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid inputs made %d requests", calls)
	}
}

func TestLookupHonorsProxyFailureAndRedactsDiagnostics(t *testing.T) {
	cause := fmt.Errorf("http://user:private-proxy-password@proxy.example: %w", proxy.ErrProxyBypass)
	var calls int
	service := New(&http.Client{Transport: reputationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, cause
	})})
	got, err := service.Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
	if !reflect.DeepEqual(got, Report{}) || !errors.Is(err, cause) || !errors.Is(err, proxy.ErrProxyBypass) || calls != 1 {
		t.Fatalf("proxy cause lost or retried: report=%+v, err=%v, calls=%d", got, err, calls)
	}
	for _, secret := range []string{"private-proxy-password", "proxy.example", "proxycheck.io", fixtureIP} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("request detail leaked: %v", err)
		}
	}
}

func TestLookupUsesProvidedProxyPolicy(t *testing.T) {
	var policyCalls int
	transport := &http.Transport{Proxy: func(req *http.Request) (*url.URL, error) {
		policyCalls++
		if req.URL.Host != "proxycheck.io" {
			t.Errorf("unexpected policy target: %s", req.URL.Host)
		}
		return nil, proxy.ErrProxyBypass
	}}
	defer transport.CloseIdleConnections()
	_, err := New(&http.Client{Transport: transport}).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
	if !errors.Is(err, proxy.ErrProxyBypass) || policyCalls != 1 {
		t.Fatalf("proxy policy changed or bypassed: err=%v, calls=%d", err, policyCalls)
	}
}

func TestLookupDoesNotFollowRedirects(t *testing.T) {
	var calls, redirects int
	original := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		redirects++
		return nil
	}, Transport: reputationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		response := reputationResponse(http.StatusFound, "private-provider-diagnostic")
		response.Header.Set("Location", "https://unexpected.invalid/redirect-target")
		return response, nil
	})}
	got, err := New(original).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
	if err == nil || !reflect.DeepEqual(got, Report{}) || calls != 1 || redirects != 0 {
		t.Fatalf("redirect followed: report=%+v, err=%v, calls=%d, redirects=%d", got, err, calls, redirects)
	}
}

func TestLookupCancellationBeforeRequest(t *testing.T) {
	var calls int
	service := New(&http.Client{Transport: reputationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})})
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		got, err := service.Lookup(ctx, netip.MustParseAddr(fixtureIP))
		cancel()
		if !reflect.DeepEqual(got, Report{}) || !errors.Is(err, want) {
			t.Fatalf("cancellation cause lost: report=%+v, err=%v", got, err)
		}
	}
	if calls != 0 {
		t.Fatalf("canceled contexts made %d requests", calls)
	}
}

func TestLookupHonorsClientAndBodyTimeouts(t *testing.T) {
	for _, readingBody := range []bool{false, true} {
		t.Run(fmt.Sprintf("body=%v", readingBody), func(t *testing.T) {
			var started atomic.Bool
			service := New(&http.Client{Timeout: 20 * time.Millisecond, Transport: reputationTransport(func(req *http.Request) (*http.Response, error) {
				if readingBody {
					return &http.Response{StatusCode: http.StatusOK, Body: &reputationDeadlineBody{ctx: req.Context(), started: &started}}, nil
				}
				started.Store(true)
				<-req.Context().Done()
				return nil, req.Context().Err()
			})})
			got, err := service.Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
			if !started.Load() || !reflect.DeepEqual(got, Report{}) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout lost: started=%v, report=%+v, err=%v", started.Load(), got, err)
			}
		})
	}
}

func TestLookupRedactsBodyReadFailure(t *testing.T) {
	cause := errors.New("private-proxy-read-diagnostic")
	service := New(&http.Client{Transport: reputationTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: reputationFailingBody{cause}}, nil
	})})
	got, err := service.Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
	if !reflect.DeepEqual(got, Report{}) || !errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("body error cause lost or leaked: report=%+v, err=%v", got, err)
	}
}

func TestLookupCancellationDuringBodyRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started atomic.Bool
	service := New(&http.Client{Transport: reputationTransport(func(req *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: http.StatusOK, Body: &reputationDeadlineBody{ctx: req.Context(), started: &started}}, nil
	})})
	got, err := service.Lookup(ctx, netip.MustParseAddr(fixtureIP))
	if !started.Load() || !reflect.DeepEqual(got, Report{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("body cancellation lost: started=%v, report=%+v, err=%v", started.Load(), got, err)
	}
}

func TestLookupRedactsInvalidEndpoint(t *testing.T) {
	service := reputationService(`{"status":"ok","` + fixtureIP + `":{}}`)
	service.endpoint = "https://user:private-endpoint-password@invalid%host"
	got, err := service.Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
	if err == nil || !reflect.DeepEqual(got, Report{}) || strings.Contains(err.Error(), "private-endpoint-password") || strings.Contains(err.Error(), "invalid%host") {
		t.Fatalf("invalid endpoint diagnostic leaked: report=%+v, err=%v", got, err)
	}
}

type reputationDeadlineBody struct {
	ctx     context.Context
	started *atomic.Bool
}

func (body *reputationDeadlineBody) Read([]byte) (int, error) {
	body.started.Store(true)
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*reputationDeadlineBody) Close() error { return nil }

type reputationFailingBody struct{ err error }

func (body reputationFailingBody) Read([]byte) (int, error) { return 0, body.err }

func (body reputationFailingBody) Close() error { return nil }

func assertReputationFailure(t *testing.T, body string) {
	t.Helper()
	got, err := reputationService(body).Lookup(context.Background(), netip.MustParseAddr(fixtureIP))
	if err == nil || !reflect.DeepEqual(got, Report{}) {
		t.Fatalf("invalid response accepted: report=%+v, err=%v", got, err)
	}
}

type reputationTransport func(*http.Request) (*http.Response, error)

func (transport reputationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func reputationResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func reputationService(body string) *Client {
	return New(&http.Client{Transport: reputationTransport(func(*http.Request) (*http.Response, error) {
		return reputationResponse(http.StatusOK, body), nil
	})})
}

func reputationPointer[T any](value T) *T { return &value }
