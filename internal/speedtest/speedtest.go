// Package speedtest measures bounded HTTP performance along a supplied route.
package speedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxDownloadBytes int64 = 16 * 1024 * 1024
	measurementLimit       = 4 * time.Second
	latencySamples         = 3
)

var (
	ErrBudgetExhausted = errors.New("speed test total budget exhausted")
	ErrInvalidResponse = errors.New("invalid speed test response")
)

// HTTPError exposes only the status code, never a service-provided body.
type HTTPError struct{ StatusCode int }

func (failure *HTTPError) Error() string {
	return fmt.Sprintf("speed test service returned HTTP %d", failure.StatusCode)
}

// Result is target-specific HTTP performance, not ISP capacity or ICMP latency.
// Availability flags distinguish an unmeasured value from a measured zero.
type Result struct {
	Target           string
	DownloadMbps     float64
	Latency          time.Duration
	Bytes            int64
	Duration         time.Duration
	Limited          bool
	LatencyMeasured  bool
	DownloadMeasured bool
}

// Attempt retains a completed latency sample even if the download fails.
type Attempt struct {
	Result  Result
	Err     error
	Skipped bool
}

// Report includes consumed response-body bytes across successful and failed
// attempts, including tiny latency probes. Nodes are always tested sequentially.
type Report struct {
	Attempts []Attempt
	Bytes    int64
	Duration time.Duration
}

type target struct {
	name      string
	endpoint  string
	fileBytes int64 // Zero selects the Cloudflare bytes-query protocol.
}

// Client preserves the supplied HTTP route for every target. No provider SDK,
// registration, external executable, or replacement direct connection is used.
type Client struct {
	client       *http.Client
	targets      []target
	limit        time.Duration
	maxBytes     int64
	totalLimit   time.Duration
	compareLimit time.Duration
	attemptLimit time.Duration
	probeLimit   time.Duration
}

func New(client *http.Client) *Client {
	service := &Client{
		targets: []target{
			{name: "Cloudflare", endpoint: "https://speed.cloudflare.com/__down"},
			{name: "OVHcloud Singapore", endpoint: "https://sin1-sgp.speedtest.network.ovh.net/files/100Mb.dat", fileBytes: 100 * 1024 * 1024},
			{name: "OVHcloud Roubaix", endpoint: "https://lil1-rbx.speedtest.network.ovh.net/files/100Mb.dat", fileBytes: 100 * 1024 * 1024},
		},
		limit: measurementLimit, maxBytes: maxDownloadBytes,
		totalLimit: 20 * time.Second, compareLimit: 30 * time.Second,
		attemptLimit: 8 * time.Second, probeLimit: 2 * time.Second,
	}
	if client != nil {
		cloned := *client
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		service.client = &cloned
	}
	return service
}

// Run stops after the first complete result by default. Compare tests every
// target serially and returns a partial-failure error if any target fails or is
// skipped. Total budgets include failed transfers; comparison has a fixed 48 MiB
// body-read budget, default mode 16 MiB. The selected transport never changes.
func (client *Client) Run(ctx context.Context, compare bool) (Report, error) {
	if client == nil || client.client == nil || client.limit <= 0 || client.maxBytes <= 0 || len(client.targets) == 0 || client.totalLimit <= 0 || client.compareLimit <= 0 || client.attemptLimit <= 0 || client.probeLimit <= 0 {
		return Report{}, errors.New("speed test HTTP client is not configured")
	}
	if ctx == nil {
		return Report{}, errors.New("speed test context is not configured")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, safeError(ctx, err)
	}
	started := time.Now()
	limit, remaining := client.totalLimit, client.maxBytes
	if compare {
		limit, remaining = client.compareLimit, client.maxBytes*3
	}
	batchContext, stop := context.WithTimeout(ctx, limit)
	defer stop()
	report := Report{}
	var failures []error
	for _, node := range client.targets {
		if ctx.Err() != nil {
			report.Duration = time.Since(started)
			return report, safeError(ctx, ctx.Err())
		}
		if batchContext.Err() != nil || remaining <= 0 {
			report.Attempts = append(report.Attempts, Attempt{Result: Result{Target: node.name}, Err: ErrBudgetExhausted, Skipped: true})
			failures = append(failures, ErrBudgetExhausted)
			continue
		}
		attemptContext, endAttempt := context.WithTimeout(batchContext, client.attemptLimit)
		before := remaining
		result, err := client.measureTarget(attemptContext, node, &remaining)
		endAttempt()
		report.Bytes += before - remaining
		report.Attempts = append(report.Attempts, Attempt{Result: result, Err: err})
		if err != nil {
			failures = append(failures, err)
		} else if !compare {
			report.Duration = time.Since(started)
			return report, nil
		}
	}
	report.Duration = time.Since(started)
	if ctx.Err() != nil {
		return report, safeError(ctx, ctx.Err())
	}
	return report, errors.Join(failures...)
}

func (client *Client) measureTarget(ctx context.Context, node target, remaining *int64) (Result, error) {
	result := Result{Target: node.name}
	latencies := make([]time.Duration, 0, latencySamples)
	for index := 0; index < latencySamples; index++ {
		size := int64(0)
		if node.fileBytes > 0 {
			size = 1
		}
		// Reserve enough body budget to reject a nonempty zero-byte response.
		if *remaining < max(1, size) {
			return result, &Failure{Stage: "latency", cause: ErrBudgetExhausted}
		}
		probeContext, endProbe := context.WithTimeout(ctx, client.probeLimit)
		started := time.Now()
		response, err := client.request(probeContext, node, size)
		if err != nil {
			endProbe()
			return result, &Failure{Stage: "latency", cause: err}
		}
		count, err := io.Copy(io.Discard, io.LimitReader(response.Body, max(1, size)))
		response.Body.Close()
		*remaining -= count
		elapsed := time.Since(started)
		if err == nil {
			err = probeContext.Err()
		}
		endProbe()
		if err != nil {
			return result, &Failure{Stage: "latency", cause: safeError(ctx, err)}
		}
		if count != size {
			return result, &Failure{Stage: "latency", cause: ErrInvalidResponse}
		}
		latencies = append(latencies, elapsed)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	result.Latency, result.LatencyMeasured = latencies[len(latencies)/2], true
	if *remaining <= 0 {
		return result, &Failure{Stage: "download", cause: ErrBudgetExhausted}
	}
	size := min(client.maxBytes, *remaining)
	downloadContext, stop := context.WithTimeout(ctx, client.limit)
	defer stop()
	started := time.Now()
	response, err := client.request(downloadContext, node, size)
	if err != nil {
		return result, &Failure{Stage: "download", cause: err}
	}
	defer response.Body.Close()
	count, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, size))
	*remaining -= count
	result.Duration, result.Bytes = time.Since(started), count
	if err := ctx.Err(); err != nil {
		return result, &Failure{Stage: "download", cause: safeError(ctx, err)}
	}
	budgetEnded := errors.Is(downloadContext.Err(), context.DeadlineExceeded)
	if readErr != nil && !budgetEnded {
		return result, &Failure{Stage: "download", cause: safeError(ctx, readErr)}
	}
	if count == 0 || result.Duration <= 0 || !budgetEnded && count != size {
		return result, &Failure{Stage: "download", cause: io.ErrUnexpectedEOF}
	}
	result.Limited, result.DownloadMeasured = budgetEnded || count == size, true
	result.DownloadMbps = float64(count) * 8 / result.Duration.Seconds() / 1_000_000
	return result, nil
}

func (client *Client) request(ctx context.Context, node target, size int64) (*http.Response, error) {
	endpoint, err := url.Parse(node.endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return nil, errors.New("could not create HTTPS speed test request")
	}
	query := endpoint.Query()
	if node.fileBytes == 0 {
		query.Set("bytes", strconv.FormatInt(size, 10))
	}
	query.Set("cacheBust", strconv.FormatInt(time.Now().UnixNano(), 10))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("could not create speed test request")
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Cache-Control", "no-cache, no-store")
	if node.fileBytes > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=0-%d", size-1))
	}
	response, err := client.client.Do(request)
	if err != nil {
		return nil, safeError(ctx, err)
	}
	if err := validateResponse(response, node, size); err != nil {
		response.Body.Close()
		return nil, err
	}
	return response, nil
}

func validateResponse(response *http.Response, node target, size int64) error {
	if response.StatusCode != http.StatusOK && !(node.fileBytes > 0 && response.StatusCode == http.StatusPartialContent) {
		return &HTTPError{StatusCode: response.StatusCode}
	}
	typeHeader := response.Header.Get("Content-Type")
	contentType, _, typeErr := mime.ParseMediaType(typeHeader)
	encoding := strings.TrimSpace(response.Header.Get("Content-Encoding"))
	if typeHeader != "" && typeErr != nil || strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "json") || strings.Contains(contentType, "xml") || encoding != "" && !strings.EqualFold(encoding, "identity") {
		return ErrInvalidResponse
	}
	expected := size
	if node.fileBytes > 0 {
		if response.StatusCode == http.StatusPartialContent {
			if response.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-%d/%d", size-1, node.fileBytes) {
				return ErrInvalidResponse
			}
		} else {
			// Some static servers ignore Range. Accept only the known test file
			// size, read the requested prefix, then close without draining it.
			expected = node.fileBytes
			if response.ContentLength < 0 {
				return ErrInvalidResponse
			}
		}
	}
	if response.ContentLength >= 0 && response.ContentLength != expected {
		return ErrInvalidResponse
	}
	return nil
}

// Failure identifies the failed stage and retains errors.Is/As behavior. Its
// message never includes a URL, credentials, transport text, or provider body.
type Failure struct {
	Stage string
	cause error
}

func (failure *Failure) Error() string { return "speed test " + failure.Stage + " failed" }
func (failure *Failure) Unwrap() error { return failure.cause }

type requestFailure struct{ cause error }

func (failure *requestFailure) Error() string { return "speed test request failed" }
func (failure *requestFailure) Unwrap() error { return failure.cause }

func safeError(ctx context.Context, err error) error {
	return &requestFailure{cause: errors.Join(err, ctx.Err())}
}
