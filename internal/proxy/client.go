// Package proxy builds HTTP clients with an explicit, fail-closed proxy policy.
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Source describes the route selected for HTTPS inspection requests.
type Source string

const (
	SourceExplicit    Source = "explicit"
	SourceEnvironment Source = "environment"
	SourceSystemProxy Source = "system-proxy"
	SourceSystemRoute Source = "system-route"
)

// Client keeps the HTTP client together with its HTTPS routing source.
type Client struct {
	HTTP     *http.Client
	Source   Source
	Detail   string
	Warnings []string
}

var errInvalidProxy = errors.New("invalid proxy URL; use http://, https://, socks5://, or socks5h:// with a valid host and port")

// ErrProxyBypass means a bypass rule blocked an otherwise proxied request.
// No direct connection is attempted when this error is returned.
var ErrProxyBypass = errors.New("automatic proxy selection is blocked by NO_PROXY; specify --proxy to inspect that proxy")

// NewClient selects an explicit, environment, or enabled desktop proxy before
// using the current system route. A selected proxy never falls back on failure.
func NewClient(explicit string, timeout time.Duration) (*Client, error) {
	return newAutoClient(explicit, timeout, os.LookupEnv, discoverSystem)
}

func newClient(explicit string, timeout time.Duration, lookupEnv func(string) (string, bool)) (*Client, error) {
	if timeout <= 0 {
		return nil, errors.New("request timeout must be greater than zero")
	}

	source := SourceSystemRoute
	var selectProxy func(*http.Request) (*url.URL, error)
	if explicit != "" {
		selected, err := parseProxy(explicit)
		if err != nil {
			return nil, err
		}
		source = SourceExplicit
		selectProxy = http.ProxyURL(selected)
	} else {
		// Capture values now rather than consulting the environment per request
		// or using net/http's process-wide cached environment resolver.
		allProxy := envValue(lookupEnv, "ALL_PROXY", "all_proxy")
		httpsProxy := envValue(lookupEnv, "HTTPS_PROXY", "https_proxy")
		if httpsProxy == "" {
			httpsProxy = allProxy
		}
		httpProxy := envValue(lookupEnv, "HTTP_PROXY", "http_proxy")
		if httpProxy == "" {
			httpProxy = allProxy
		}
		noProxy := strings.Split(envValue(lookupEnv, "NO_PROXY", "no_proxy"), ",")

		var httpsURL, httpURL *url.URL
		var httpError error
		if httpsProxy != "" {
			var err error
			httpsURL, err = parseProxy(httpsProxy)
			if err != nil {
				return nil, err
			}
			source = SourceEnvironment
		}
		if httpProxy != "" {
			// An invalid HTTP-only setting must not block an HTTPS-only run.
			httpURL, httpError = parseProxy(httpProxy)
		}

		selectProxy = func(req *http.Request) (*url.URL, error) {
			var selected *url.URL
			switch req.URL.Scheme {
			case "https":
				selected = httpsURL
			case "http":
				if httpError != nil {
					return nil, httpError
				}
				selected = httpURL
			}
			if selected != nil && matchesNoProxy(req.URL, noProxy) {
				return nil, ErrProxyBypass
			}
			return selected, nil
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = selectProxy
	transport.DialContext = (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.TLSHandshakeTimeout = timeout
	transport.ResponseHeaderTimeout = timeout
	return &Client{
		Source: source,
		Detail: routeDetail(source),
		HTTP: &http.Client{
			Transport: &safeTransport{transport: transport},
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func envValue(lookupEnv func(string) (string, bool), names ...string) string {
	for _, name := range names {
		if value, ok := lookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseProxy(raw string) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, errInvalidProxy
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		// url.Parse errors can include the unredacted input.
		return nil, errInvalidProxy
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errInvalidProxy
	}
	if parsed.Hostname() == "" || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errInvalidProxy
	}
	if strings.ContainsAny(parsed.Hostname(), " \t\r\n") {
		return nil, errInvalidProxy
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errInvalidProxy
		}
	}
	if (parsed.Scheme == "socks5" || parsed.Scheme == "socks5h") && parsed.User != nil {
		password, _ := parsed.User.Password()
		if len(parsed.User.Username()) > 255 || len(password) > 255 {
			return nil, errInvalidProxy
		}
	}
	return parsed, nil
}

// matchesNoProxy checks literal IPs, CIDRs, domain suffixes, and optional ports.
// It never resolves a hostname locally merely to evaluate a bypass rule.
func matchesNoProxy(target *url.URL, rules []string) bool {
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	address, addressError := netip.ParseAddr(host)
	if addressError == nil {
		address = address.Unmap()
	}
	for _, value := range rules {
		rule := strings.ToLower(strings.TrimSpace(value))
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if prefix, err := netip.ParsePrefix(rule); err == nil {
			if addressError == nil && prefix.Contains(address) {
				return true
			}
			continue
		}
		if ruleHost, rulePort, err := net.SplitHostPort(rule); err == nil {
			if rulePort != port {
				continue
			}
			rule = ruleHost
		}
		rule = strings.TrimSuffix(strings.Trim(rule, "[]"), ".")
		if ruleAddress, err := netip.ParseAddr(rule); err == nil {
			if addressError == nil && ruleAddress.Unmap() == address {
				return true
			}
			continue
		}
		if strings.HasPrefix(rule, "*.") {
			rule = rule[1:]
		}
		if strings.HasPrefix(rule, ".") {
			if strings.HasSuffix(host, rule) {
				return true
			}
		} else if host == rule || strings.HasSuffix(host, "."+rule) {
			return true
		}
	}
	return false
}

// safeTransport avoids exposing proxy credentials through network error strings.
// The original cause remains available to errors.Is/errors.As.
type safeTransport struct {
	transport *http.Transport
}

func (t *safeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.transport.RoundTrip(req)
	if err == nil {
		return response, nil
	}
	if errors.Is(err, ErrProxyBypass) || errors.Is(err, errInvalidProxy) {
		return response, err
	}
	return response, &requestError{cause: err}
}

func (t *safeTransport) CloseIdleConnections() {
	t.transport.CloseIdleConnections()
}

type requestError struct {
	cause error
}

func (e *requestError) Error() string {
	if errors.Is(e.cause, context.Canceled) {
		return "network request canceled"
	}
	if e.Timeout() {
		return "network request timed out"
	}
	return "network request failed"
}

func (e *requestError) Unwrap() error { return e.cause }

func (e *requestError) Timeout() bool {
	var networkError net.Error
	return errors.Is(e.cause, context.DeadlineExceeded) || (errors.As(e.cause, &networkError) && networkError.Timeout())
}

func (e *requestError) Temporary() bool { return false }
