// Package geolocation looks up the estimated location of an observed IP address.
package geolocation

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

const maxResponseBytes = 16 * 1024

// Location contains the available country, region, and city of an IP's egress.
// Empty fields mean that the provider did not supply that level of detail.
type Location struct {
	Country string
	Region  string
	City    string
}

// Client uses an existing HTTP client's route for explicit-IP lookups.
// It is safe to call Lookup concurrently after construction.
type Client struct {
	client   *http.Client
	endpoint string
}

// New preserves the supplied transport and timeout, while disabling redirects.
// Passing nil leaves lookups unconfigured; it never enables a direct fallback.
func New(client *http.Client) *Client {
	service := &Client{endpoint: "https://ipwho.is/"}
	if client != nil {
		cloned := *client
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		service.client = &cloned
	}
	return service
}

// Lookup queries exactly ip, independently of the source IP of the lookup
// connection. Missing location fields are valid and do not fail the lookup.
func (c *Client) Lookup(ctx context.Context, ip netip.Addr) (Location, error) {
	if !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() {
		return Location{}, errors.New("geolocation requires a valid unscoped IP address")
	}
	if c == nil || c.client == nil {
		return Location{}, errors.New("geolocation HTTP client is not configured")
	}
	if ctx == nil {
		return Location{}, errors.New("geolocation context is not configured")
	}
	if err := ctx.Err(); err != nil {
		return Location{}, requestError(ctx, err)
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return Location{}, &lookupFailure{message: "could not create geolocation request", cause: err}
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + ip.String()
	endpoint.RawPath = ""
	query := endpoint.Query()
	query.Set("fields", "ip,success,country,region,city")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Location{}, &lookupFailure{message: "could not create geolocation request", cause: err}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return Location{}, requestError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Location{}, fmt.Errorf("geolocation service returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Location{}, requestError(ctx, err)
	}
	if len(body) > maxResponseBytes {
		return Location{}, errors.New("geolocation response is too large")
	}
	var payload struct {
		IP      string `json:"ip"`
		Success *bool  `json:"success"`
		Country string `json:"country"`
		Region  string `json:"region"`
		City    string `json:"city"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return Location{}, errors.New("invalid geolocation response")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Location{}, errors.New("invalid geolocation response")
	}
	if payload.Success == nil || !*payload.Success {
		return Location{}, errors.New("geolocation service did not confirm a successful lookup")
	}
	returnedIP, err := netip.ParseAddr(payload.IP)
	if err != nil || returnedIP != ip {
		return Location{}, errors.New("geolocation response did not match the requested IP address")
	}
	return Location{
		Country: locationText(payload.Country),
		Region:  locationText(payload.Region),
		City:    locationText(payload.City),
	}, nil
}

func locationText(value string) string {
	return strings.TrimSpace(strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value))
}

func requestError(ctx context.Context, err error) error {
	cause := errors.Join(err, ctx.Err())
	if errors.Is(cause, context.Canceled) {
		return &lookupFailure{message: "geolocation request canceled", cause: cause}
	}
	var timeout net.Error
	if errors.Is(cause, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return &lookupFailure{
			message: "geolocation request timed out",
			cause:   errors.Join(cause, context.DeadlineExceeded),
		}
	}
	return &lookupFailure{message: "geolocation request failed", cause: cause}
}

// Keep diagnostics safe to display while retaining causes for errors.Is/As.
type lookupFailure struct {
	message string
	cause   error
}

func (e *lookupFailure) Error() string { return e.message }

func (e *lookupFailure) Unwrap() error { return e.cause }
