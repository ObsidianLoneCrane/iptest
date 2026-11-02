// Package egress observes public IP addresses through a supplied HTTP client.
package egress

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
)

// Family identifies the address family observed at the remote endpoint.
type Family string

const (
	IPv4 Family = "ipv4"
	IPv6 Family = "ipv6"
)

const maxResponseBytes = 4 * 1024

// Observation records one address returned by an address-family-specific probe.
type Observation struct {
	Family Family
	IP     netip.Addr
}

// Detector probes a public IP echo service without changing the client's route.
// It is safe to call Detect concurrently after construction.
type Detector struct {
	client  *http.Client
	ipv4URL string
	ipv6URL string
}

// New retains the supplied client's transport and timeout, while disabling
// redirects. A nil client remains unconfigured instead of using a direct client.
func New(client *http.Client) *Detector {
	detector := &Detector{
		ipv4URL: "https://api.ipify.org?format=json",
		ipv6URL: "https://api6.ipify.org?format=json",
	}
	if client != nil {
		cloned := *client
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		detector.client = &cloned
	}
	return detector
}

// Detect observes an IPv4 or IPv6 public egress address. It never falls back to
// another address family, client, or network path after a failed request.
func (d *Detector) Detect(ctx context.Context, family Family) (Observation, error) {
	if family != IPv4 && family != IPv6 {
		return Observation{}, errors.New("unsupported IP family")
	}
	if d == nil || d.client == nil {
		return Observation{}, errors.New("egress HTTP client is not configured")
	}
	endpoint := d.ipv4URL
	if family == IPv6 {
		endpoint = d.ipv6URL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Observation{}, errors.New("could not create egress request")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return Observation{}, requestError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Observation{}, fmt.Errorf("egress service returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Observation{}, requestError(ctx, err)
	}
	if len(body) > maxResponseBytes {
		return Observation{}, errors.New("egress response is too large")
	}
	var payload struct {
		IP string `json:"ip"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return Observation{}, errors.New("invalid egress response")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Observation{}, errors.New("invalid egress response")
	}
	ip, err := netip.ParseAddr(payload.IP)
	if err != nil || ip.Zone() != "" || ip.Is4In6() {
		return Observation{}, errors.New("egress response did not contain a valid IP address")
	}
	if (family == IPv4 && !ip.Is4()) || (family == IPv6 && !ip.Is6()) {
		return Observation{}, errors.New("egress response used the wrong IP family")
	}
	if !publicAddress(ip) {
		return Observation{}, errors.New("egress response did not contain a public IP address")
	}
	return Observation{Family: family, IP: ip}, nil
}

func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("egress request failed: %w", ctx.Err())
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("egress request failed: %w", context.Canceled)
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return fmt.Errorf("egress request failed: %w", context.DeadlineExceeded)
	}
	// Transport errors can contain proxy credentials, URLs, or response text.
	return &transportFailure{cause: err}
}

type transportFailure struct {
	cause error
}

func (e *transportFailure) Error() string { return "egress request failed" }

func (e *transportFailure) Unwrap() error { return e.cause }
