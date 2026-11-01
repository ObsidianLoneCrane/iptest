package reputation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
)

const maxResponseBytes = 128 * 1024

var (
	ErrDenied      = errors.New("reputation service denied anonymous access")
	ErrRateLimited = errors.New("reputation service rate limit reached")
)

// Client reuses the caller's route and timeout without following redirects.
// It is safe for concurrent lookups after construction.
type Client struct {
	client   *http.Client
	endpoint string
}

// New configures anonymous HTTPS queries without keys, accounts, or email.
// A nil client leaves the service unconfigured; it never selects another route.
func New(client *http.Client) *Client {
	service := &Client{endpoint: "https://proxycheck.io/v3/"}
	if client != nil {
		cloned := *client
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		service.client = &cloned
	}
	return service
}

// Lookup queries exactly ip. Optional fields are retained independently when
// valid. An invalid envelope, access denial, or request failure returns no facts.
func (client *Client) Lookup(ctx context.Context, ip netip.Addr) (Report, error) {
	if !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() {
		return Report{}, errors.New("reputation requires a valid unscoped IP address")
	}
	if client == nil || client.client == nil {
		return Report{}, errors.New("reputation HTTP client is not configured")
	}
	if ctx == nil {
		return Report{}, errors.New("reputation context is not configured")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, requestError(ctx, err)
	}
	endpoint, err := url.Parse(client.endpoint)
	if err != nil {
		return Report{}, &lookupFailure{message: "could not create reputation request", cause: err}
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + ip.String()
	endpoint.RawPath = ""
	query := endpoint.Query()
	query.Set("ver", "24-June-2026")
	query.Set("tag", "0")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Report{}, &lookupFailure{message: "could not create reputation request", cause: err}
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return Report{}, requestError(ctx, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return Report{}, ErrDenied
	case http.StatusTooManyRequests:
		return Report{}, ErrRateLimited
	default:
		return Report{}, fmt.Errorf("reputation service returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Report{}, requestError(ctx, err)
	}
	if len(body) > maxResponseBytes {
		return Report{}, errors.New("reputation response is too large")
	}
	return parseReport(body, ip)
}

func parseReport(body []byte, ip netip.Addr) (Report, error) {
	invalid := errors.New("invalid reputation response")
	if !unambiguousJSON(body) {
		return Report{}, invalid
	}
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return Report{}, invalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Report{}, invalid
	}
	var status string
	if err := json.Unmarshal(payload["status"], &status); err != nil {
		return Report{}, invalid
	}
	if status == "denied" {
		return Report{}, ErrDenied
	}
	if status != "ok" && status != "warning" {
		return Report{}, errors.New("reputation service did not confirm a successful lookup")
	}
	var entry json.RawMessage
	for key, raw := range payload {
		returnedIP, err := netip.ParseAddr(key)
		if err != nil {
			continue
		}
		if returnedIP != ip || entry != nil {
			return Report{}, errors.New("reputation response did not match the requested IP address")
		}
		entry = raw
	}
	if entry == nil {
		return Report{}, errors.New("reputation response did not match the requested IP address")
	}
	wire := optionalObject(entry)
	if wire == nil {
		return Report{}, invalid
	}
	report := Report{Warning: status == "warning"}
	if network := optionalObject(wire["network"]); network != nil {
		var networkType string
		if json.Unmarshal(network["type"], &networkType) == nil {
			report.NetworkType = strings.TrimSpace(networkType)
		}
	}
	if devices := optionalObject(wire["device_estimate"]); devices != nil {
		report.EstimatedDevices = optionalCount(devices["address"])
	}
	if detections := optionalObject(wire["detections"]); detections != nil {
		report.Detections = Detections{
			Compromised: optionalBool(detections["compromised"]),
			Proxy:       optionalBool(detections["proxy"]),
			VPN:         optionalBool(detections["vpn"]),
			Tor:         optionalBool(detections["tor"]),
			Hosting:     optionalBool(detections["hosting"]),
			Scraper:     optionalBool(detections["scraper"]),
		}
	}
	report.AttackHistory = optionalHistory(wire["attack_history"])
	return report, nil
}

func optionalObject(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

func optionalCount(raw json.RawMessage) *int64 {
	var count *int64
	if json.Unmarshal(raw, &count) != nil || count == nil || *count < 0 {
		return nil
	}
	return count
}

func optionalBool(raw json.RawMessage) *bool {
	var value *bool
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func optionalHistory(raw json.RawMessage) map[string]int64 {
	if counts := optionalObject(raw); counts != nil {
		var history map[string]int64
		if len(counts) == 0 {
			return map[string]int64{}
		}
		for kind, rawCount := range counts {
			if count := optionalCount(rawCount); count != nil {
				if history == nil {
					history = make(map[string]int64)
				}
				history[kind] = *count
			}
		}
		return history
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) == nil && entries != nil && len(entries) == 0 {
		return map[string]int64{}
	}
	return nil
}

// Duplicate keys can hide source facts behind a later false/zero value.
// Reject them at every level rather than relying on encoding/json's overwrite
// behavior. The depth limit also bounds validation of untrusted nested values.
func unambiguousJSON(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 64 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return true
		}
		switch delimiter {
		case '{':
			seen := make(map[string]bool)
			for decoder.More() {
				key, err := decoder.Token()
				name, isString := key.(string)
				if err != nil || !isString {
					return false
				}
				name = foldedJSONKey(name)
				if seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
		case '[':
			for decoder.More() {
				if !value(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		closing, err := decoder.Token()
		return err == nil && ((delimiter == '{' && closing == json.Delim('}')) ||
			(delimiter == '[' && closing == json.Delim(']')))
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

// encoding/json matches struct fields using Unicode simple case folding.
// Canonicalize the same equivalence classes so aliases such as Compromised
// cannot overwrite compromised after passing the duplicate-key check.
func foldedJSONKey(key string) string {
	return strings.Map(func(character rune) rune {
		minimum := character
		for next := unicode.SimpleFold(character); next != character; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, key)
}

func requestError(ctx context.Context, err error) error {
	cause := errors.Join(err, ctx.Err())
	if errors.Is(cause, context.Canceled) {
		return &lookupFailure{message: "reputation request canceled", cause: cause}
	}
	var timeout net.Error
	if errors.Is(cause, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return &lookupFailure{message: "reputation request timed out", cause: errors.Join(cause, context.DeadlineExceeded)}
	}
	return &lookupFailure{message: "reputation request failed", cause: cause}
}

type lookupFailure struct {
	message string
	cause   error
}

func (err *lookupFailure) Error() string { return err.message }
func (err *lookupFailure) Unwrap() error { return err.cause }
