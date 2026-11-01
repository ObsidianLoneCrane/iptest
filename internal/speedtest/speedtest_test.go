package speedtest

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
)

type transportFunc func(*http.Request) (*http.Response, error)

// Existing protocol tests isolate one backend; failover has its own fixtures.
func singleClient(httpClient *http.Client) *Client {
	client := New(httpClient)
	client.targets = client.targets[:1]
	return client
}

func measure(client *Client, ctx context.Context) (Result, error) {
	report, err := client.Run(ctx, false)
	if len(report.Attempts) == 0 {
		return Result{}, err
	}
	return report.Attempts[len(report.Attempts)-1].Result, err
}

func (transport transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func response(bytes int64, body io.Reader) *http.Response {
	return &http.Response{StatusCode: 200, ContentLength: bytes, Header: make(http.Header), Body: io.NopCloser(body)}
}

func fixtureClient(download func(*http.Request) (*http.Response, error)) *Client {
	return singleClient(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("bytes") == "0" {
			return response(0, strings.NewReader("")), nil
		}
		return download(request)
	})})
}

func TestMeasurePreservesRouteAndBoundsRequests(t *testing.T) {
	var sizes []string
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "speed.cloudflare.com" || request.URL.Path != "/__down" || request.Method != http.MethodGet {
			t.Errorf("unexpected measurement request: %s", request.URL)
		}
		if request.Header.Get("Accept-Encoding") != "identity" || request.URL.Query().Get("cacheBust") == "" || request.URL.Query().Has("key") {
			t.Error("measurement request did not disable compression/cache or unexpectedly used a key")
		}
		size := request.URL.Query().Get("bytes")
		sizes = append(sizes, size)
		if size == "0" {
			time.Sleep(time.Millisecond)
			return response(0, strings.NewReader("")), nil
		}
		time.Sleep(2 * time.Millisecond)
		return response(1024, strings.NewReader(strings.Repeat("x", 1024))), nil
	})
	original := &http.Client{Transport: &transport, Timeout: 3 * time.Second}
	client := singleClient(original)
	client.maxBytes = 1024
	if client.client == original || client.client.Transport != original.Transport || client.client.Timeout != original.Timeout {
		t.Fatal("measurement changed supplied route or timeout")
	}
	result, err := measure(client, context.Background())
	if err != nil || strings.Join(sizes, ",") != "0,0,0,1024" || result.Bytes != 1024 || result.Duration <= 0 || result.Latency <= 0 || result.Target != "Cloudflare" || !result.Limited {
		t.Fatalf("result=%+v,error=%v,sizes=%v", result, err, sizes)
	}
	want := float64(result.Bytes) * 8 / result.Duration.Seconds() / 1_000_000
	if math.Abs(result.DownloadMbps-want) > 0.000001 {
		t.Fatalf("download=%v,want=%v", result.DownloadMbps, want)
	}
}

func TestMeasureDoesNotFallbackOrExposeTransportSecrets(t *testing.T) {
	for _, cause := range []error{proxy.ErrProxyBypass, errors.New("password@private-proxy.example:9876")} {
		calls := 0
		client := singleClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, cause })})
		_, err := measure(client, context.Background())
		if err == nil || !errors.Is(err, cause) || calls != 1 || strings.Contains(err.Error(), "private-proxy") || strings.Contains(err.Error(), "password") {
			t.Fatalf("error=%v,calls=%d", err, calls)
		}
	}
}

func TestMeasureRejectsRedirectWithoutFollowing(t *testing.T) {
	calls := 0
	client := singleClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		out := response(0, strings.NewReader(""))
		out.StatusCode = 302
		out.Header.Set("Location", "https://unexpected.example/")
		return out, nil
	})})
	if _, err := measure(client, context.Background()); err == nil || calls != 1 {
		t.Fatalf("redirect was accepted or followed: error=%v,calls=%d", err, calls)
	}
}

func TestMeasureRejectsInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name string
		make func() *http.Response
	}{
		{"HTTP failure", func() *http.Response { out := response(0, strings.NewReader("")); out.StatusCode = 429; return out }},
		{"HTML", func() *http.Response {
			out := response(1024, strings.NewReader(strings.Repeat("x", 1024)))
			out.Header.Set("Content-Type", "text/html")
			return out
		}},
		{"JSON", func() *http.Response {
			out := response(1024, strings.NewReader(strings.Repeat("x", 1024)))
			out.Header.Set("Content-Type", "application/json")
			return out
		}},
		{"compressed", func() *http.Response {
			out := response(1024, strings.NewReader(strings.Repeat("x", 1024)))
			out.Header.Set("Content-Encoding", "gzip")
			return out
		}},
		{"wrong declared size", func() *http.Response { return response(20, strings.NewReader(strings.Repeat("x", 20))) }},
		{"truncated body", func() *http.Response { return response(1024, strings.NewReader(strings.Repeat("x", 500))) }},
		{"empty body", func() *http.Response { return response(1024, strings.NewReader("")) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := fixtureClient(func(*http.Request) (*http.Response, error) { return test.make(), nil })
			client.maxBytes = 1024
			if result, err := measure(client, context.Background()); err == nil || result.DownloadMeasured {
				t.Fatalf("invalid response accepted: result=%+v,error=%v", result, err)
			}
		})
	}
	client := singleClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(-1, strings.NewReader("not-empty")), nil })})
	if _, err := measure(client, context.Background()); err == nil {
		t.Fatal("nonempty latency response accepted")
	}
}

type budgetBody struct {
	ctx     context.Context
	first   bool
	started chan struct{}
}

func (body *budgetBody) Read(buffer []byte) (int, error) {
	if !body.first {
		body.first = true
		if body.started != nil {
			close(body.started)
		}
		return copy(buffer, strings.Repeat("x", 128)), nil
	}
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func TestMeasureRetainsBytesWhenOwnDownloadBudgetEnds(t *testing.T) {
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		return response(1024, &budgetBody{ctx: request.Context()}), nil
	})
	client.maxBytes = 1024
	client.limit = 25 * time.Millisecond
	result, err := measure(client, context.Background())
	if err != nil || result.Bytes != 128 || result.DownloadMbps <= 0 || !result.Limited {
		t.Fatalf("budget-limited download lost valid bytes: result=%+v,error=%v", result, err)
	}
}

func TestMeasureExternalCancellationDoesNotBecomeSuccessfulPartial(t *testing.T) {
	started := make(chan struct{})
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		return response(1024, &budgetBody{ctx: request.Context(), started: started}), nil
	})
	client.maxBytes = 1024
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := measure(client, ctx); done <- err }()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("download did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("download ignored cancellation")
	}
}

func TestMeasurePreservesClientTimeoutDuringBodyRead(t *testing.T) {
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		return response(1024, &budgetBody{ctx: request.Context()}), nil
	})
	client.maxBytes = 1024
	client.client.Timeout = 20 * time.Millisecond
	if result, err := measure(client, context.Background()); !errors.Is(err, context.DeadlineExceeded) || result.DownloadMeasured {
		t.Fatalf("client timeout became a successful partial: result=%+v,error=%v", result, err)
	}
}

type countingBody struct{ read int64 }

func (body *countingBody) Read(buffer []byte) (int, error) {
	time.Sleep(2 * time.Millisecond)
	for i := range buffer {
		buffer[i] = 'x'
	}
	body.read += int64(len(buffer))
	return len(buffer), nil
}

func TestMeasureDoesNotReadPastByteCap(t *testing.T) {
	body := &countingBody{}
	client := fixtureClient(func(*http.Request) (*http.Response, error) { return response(-1, body), nil })
	client.maxBytes = 1024
	result, err := measure(client, context.Background())
	if err != nil || body.read != 1024 || result.Bytes != 1024 {
		t.Fatalf("byte cap not enforced: read=%d,result=%+v,error=%v", body.read, result, err)
	}
}

func TestMeasureRejectsUnconfiguredOrCanceledRequests(t *testing.T) {
	for _, client := range []*Client{nil, singleClient(nil)} {
		if _, err := measure(client, context.Background()); err == nil {
			t.Fatal("nil client accepted")
		}
	}
	calls := 0
	client := singleClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(0, strings.NewReader("")), nil })})
	if _, err := measure(client, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := measure(client, ctx); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled request executed: %v,calls=%d", err, calls)
	}
}
