package speedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/proxy"
)

func rangeResponse(request *http.Request, total int64, partial bool) *http.Response {
	time.Sleep(time.Millisecond)
	var end int64
	fmt.Sscanf(request.Header.Get("Range"), "bytes=0-%d", &end)
	size := end + 1
	result := response(size, strings.NewReader(strings.Repeat("x", int(size))))
	if partial {
		result.StatusCode = http.StatusPartialContent
		result.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", end, total))
	} else {
		result.ContentLength = total
	}
	return result
}

func TestAutomaticFailoverUsesSameTransportAndStopsAfterSuccess(t *testing.T) {
	for _, partial := range []bool{true, false} {
		t.Run(fmt.Sprint("range supported: ", partial), func(t *testing.T) {
			var hosts []string
			transport := transportFunc(func(request *http.Request) (*http.Response, error) {
				hosts = append(hosts, request.URL.Host)
				if request.URL.Scheme != "https" || request.Header.Get("Accept-Encoding") != "identity" || request.URL.Query().Get("cacheBust") == "" {
					t.Error("insecure, compressed, or cacheable measurement request")
				}
				if request.URL.Host == "speed.cloudflare.com" {
					return nil, io.EOF
				}
				if request.URL.Host != "sin1-sgp.speedtest.network.ovh.net" || request.URL.Path != "/files/100Mb.dat" || request.URL.Query().Has("bytes") {
					t.Fatalf("unexpected fallback target or protocol: %s", request.URL)
				}
				out := rangeResponse(request, 100*1024*1024, partial)
				return out, nil
			})
			original := &http.Client{Transport: &transport, Timeout: time.Second}
			client := New(original)
			client.maxBytes = 1024
			report, err := client.Run(context.Background(), false)
			if err != nil || len(report.Attempts) != 2 || len(hosts) != 5 || client.client.Transport != original.Transport || client.client.Timeout != original.Timeout {
				t.Fatalf("failover: report=%+v error=%v hosts=%v", report, err, hosts)
			}
			first, last := report.Attempts[0], report.Attempts[1]
			if !errors.Is(first.Err, io.EOF) || last.Err != nil || !last.Result.DownloadMeasured || !last.Result.LatencyMeasured || last.Result.Bytes != 1021 || report.Bytes != 1024 {
				t.Fatalf("incorrect fallback accounting or flags: %+v", report)
			}
		})
	}
}

func TestComparisonTestsEveryNodeSeriallyAndRetainsPartialResults(t *testing.T) {
	var hosts []string
	client := New(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		if request.URL.Host == "speed.cloudflare.com" {
			return nil, io.EOF
		}
		out := rangeResponse(request, 100*1024*1024, true)
		if request.URL.Host == "lil1-rbx.speedtest.network.ovh.net" && request.Header.Get("Range") != "bytes=0-0" {
			out.Body = io.NopCloser(strings.NewReader("xx"))
		}
		return out, nil
	})})
	client.maxBytes = 1024
	report, err := client.Run(context.Background(), true)
	if err == nil || len(report.Attempts) != 3 || !report.Attempts[1].Result.DownloadMeasured || !report.Attempts[2].Result.LatencyMeasured || report.Attempts[2].Result.DownloadMeasured || report.Bytes != 1032 {
		t.Fatalf("comparison failed to retain individual results: report=%+v error=%v", report, err)
	}
	want := "speed.cloudflare.com," + strings.Repeat("sin1-sgp.speedtest.network.ovh.net,", 4) + strings.TrimSuffix(strings.Repeat("lil1-rbx.speedtest.network.ovh.net,", 4), ",")
	if strings.Join(hosts, ",") != want {
		t.Fatalf("targets did not run sequentially: %v", hosts)
	}
}

func TestFailedDownloadConsumesSharedByteBudgetAndPreservesLatency(t *testing.T) {
	var sizes []int64
	client := New(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "speed.cloudflare.com" {
			size, _ := strconv.ParseInt(request.URL.Query().Get("bytes"), 10, 64)
			if size == 0 {
				return response(0, strings.NewReader("")), nil
			}
			return response(size, strings.NewReader(strings.Repeat("x", 400))), nil
		}
		out := rangeResponse(request, 100*1024*1024, true)
		sizes = append(sizes, out.ContentLength)
		return out, nil
	})})
	client.maxBytes = 1024
	report, err := client.Run(context.Background(), false)
	if err != nil || report.Bytes != 1024 || len(sizes) != 4 || sizes[3] != 621 || !report.Attempts[0].Result.LatencyMeasured || report.Attempts[0].Result.DownloadMeasured || report.Attempts[1].Result.Bytes != 621 {
		t.Fatalf("failed transfer escaped total budget: report=%+v sizes=%v err=%v", report, sizes, err)
	}
}

func TestComparisonTotalByteBudgetIsFiniteIncludingProbes(t *testing.T) {
	client := New(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		time.Sleep(time.Millisecond)
		if request.URL.Host != "speed.cloudflare.com" {
			return rangeResponse(request, 100*1024*1024, true), nil
		}
		size, _ := strconv.ParseInt(request.URL.Query().Get("bytes"), 10, 64)
		return response(size, strings.NewReader(strings.Repeat("x", int(size)))), nil
	})})
	client.maxBytes = 1024
	report, err := client.Run(context.Background(), true)
	if err != nil || report.Bytes != 3072 || report.Attempts[2].Result.Bytes != 1018 {
		t.Fatalf("comparison budget exceeded or not shared: report=%+v err=%v", report, err)
	}
}

func TestExhaustedByteBudgetSkipsRemainingTargets(t *testing.T) {
	calls := 0
	client := New(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(-1, strings.NewReader("invalid nonempty latency payload")), nil
	})})
	client.maxBytes = 1
	report, err := client.Run(context.Background(), false)
	if !errors.Is(err, ErrBudgetExhausted) || calls != 1 || report.Bytes != 1 || len(report.Attempts) != 3 || !report.Attempts[1].Skipped || !report.Attempts[2].Skipped {
		t.Fatalf("exhausted budget was reset: report=%+v calls=%d err=%v", report, calls, err)
	}
}

func TestOverallDeadlineSkipsTargetsAndNeverReportsSuccessfulPartial(t *testing.T) {
	calls := 0
	client := New(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	client.totalLimit = 20 * time.Millisecond
	started := time.Now()
	report, err := client.Run(context.Background(), false)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrBudgetExhausted) || calls != 1 || !report.Attempts[1].Skipped || time.Since(started) > time.Second {
		t.Fatalf("overall deadline not enforced: report=%+v calls=%d err=%v", report, calls, err)
	}
}

func TestPerTargetTimeoutAllowsNextTargetWithoutChangingRoute(t *testing.T) {
	client := New(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "speed.cloudflare.com" {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		return rangeResponse(request, 100*1024*1024, true), nil
	})})
	client.maxBytes = 1024
	client.attemptLimit = 20 * time.Millisecond
	report, err := client.Run(context.Background(), false)
	if err != nil || len(report.Attempts) != 2 || !errors.Is(report.Attempts[0].Err, context.DeadlineExceeded) || !report.Attempts[1].Result.DownloadMeasured {
		t.Fatalf("target timeout suppressed fallback: report=%+v err=%v", report, err)
	}
}

func TestAllTargetsKeepSelectedRouteFailuresAndSafeDiagnostics(t *testing.T) {
	for _, cause := range []error{proxy.ErrProxyBypass, errors.New("https://user:secret@private-proxy.invalid/"), io.EOF} {
		calls := 0
		client := New(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, cause
		})})
		report, err := client.Run(context.Background(), false)
		if !errors.Is(err, cause) || calls != 3 || len(report.Attempts) != 3 || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-proxy") {
			t.Fatalf("route changed, cause lost, or secret exposed: report=%+v err=%v calls=%d", report, err, calls)
		}
	}
}

func TestStaticFileRejectsUnexpectedRangeAndContent(t *testing.T) {
	for _, mutate := range []func(*http.Response){
		func(out *http.Response) { out.Header.Set("Content-Range", "bytes 0-0/1") },
		func(out *http.Response) { out.ContentLength = 100 },
		func(out *http.Response) { out.Header.Set("Content-Type", "text/html") },
		func(out *http.Response) { out.Header.Set("Content-Encoding", "gzip") },
		func(out *http.Response) { out.StatusCode = 200; out.ContentLength = -1 },
		func(out *http.Response) { out.StatusCode = 200; out.ContentLength = 123 },
	} {
		client := New(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			out := rangeResponse(request, 100*1024*1024, true)
			mutate(out)
			return out, nil
		})})
		client.targets = client.targets[1:2]
		report, err := client.Run(context.Background(), false)
		if err == nil || !errors.Is(err, ErrInvalidResponse) || report.Bytes != 0 || report.Attempts[0].Result.LatencyMeasured {
			t.Fatalf("invalid file response accepted: %+v error=%v", report, err)
		}
	}
}

func TestAutomaticModeStopsWhenPrimarySucceeds(t *testing.T) {
	calls := 0
	client := New(&http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "speed.cloudflare.com" {
			t.Fatal("backup contacted after primary success")
		}
		time.Sleep(time.Millisecond)
		size, _ := strconv.ParseInt(request.URL.Query().Get("bytes"), 10, 64)
		return response(size, strings.NewReader(strings.Repeat("x", int(size)))), nil
	})})
	client.maxBytes = 1024
	report, err := client.Run(context.Background(), false)
	if err != nil || calls != 4 || len(report.Attempts) != 1 || !report.Attempts[0].Result.DownloadMeasured {
		t.Fatalf("primary success did not stop automatic mode: %+v err=%v calls=%d", report, err, calls)
	}
}

func TestInvocationCancellationStopsBeforeAnyBackup(t *testing.T) {
	for _, compare := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		client := New(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			cancel()
			return nil, context.Canceled
		})})
		report, err := client.Run(ctx, compare)
		cancel()
		if !errors.Is(err, context.Canceled) || calls != 1 || len(report.Attempts) > 1 {
			t.Fatalf("cancellation started a backup: %+v err=%v calls=%d", report, err, calls)
		}
	}
}
