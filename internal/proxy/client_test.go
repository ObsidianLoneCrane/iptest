package proxy

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func environment(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestRoutingSelection(t *testing.T) {
	tests := []struct {
		name      string
		explicit  string
		env       map[string]string
		source    Source
		httpsHost string
		httpHost  string
	}{
		{"no proxy", "", nil, SourceSystemRoute, "", ""},
		{"HTTP only does not proxy HTTPS", "", map[string]string{"HTTP_PROXY": "http://http.example:8080"}, SourceSystemRoute, "", "http.example:8080"},
		{"HTTPS proxy", "", map[string]string{"HTTPS_PROXY": "http://https.example:8080"}, SourceEnvironment, "https.example:8080", ""},
		{"lowercase HTTPS", "", map[string]string{"https_proxy": "socks5://lower.example:1080"}, SourceEnvironment, "lower.example:1080", ""},
		{"ALL fallback", "", map[string]string{"ALL_PROXY": "socks5h://all.example:1080"}, SourceEnvironment, "all.example:1080", "all.example:1080"},
		{"lowercase ALL", "", map[string]string{"all_proxy": "http://all.example:8080"}, SourceEnvironment, "all.example:8080", "all.example:8080"},
		{"scheme variables beat ALL", "", map[string]string{"HTTP_PROXY": "http://http.example:8080", "HTTPS_PROXY": "http://https.example:8080", "ALL_PROXY": "http://all.example:8080"}, SourceEnvironment, "https.example:8080", "http.example:8080"},
		{"uppercase wins", "", map[string]string{"HTTPS_PROXY": "http://upper.example:8080", "https_proxy": "http://lower.example:8080", "ALL_PROXY": "http://all-upper.example:8080", "all_proxy": "http://all-lower.example:8080"}, SourceEnvironment, "upper.example:8080", "all-upper.example:8080"},
		{"empty uppercase uses lowercase", "", map[string]string{"HTTPS_PROXY": "", "https_proxy": "http://lower.example:8080"}, SourceEnvironment, "lower.example:8080", ""},
		{"explicit wins over invalid environment", "socks5://explicit.example:1080", map[string]string{"HTTPS_PROXY": "http://invalid:bad", "ALL_PROXY": "bad://invalid", "NO_PROXY": "*"}, SourceExplicit, "explicit.example:1080", "explicit.example:1080"},
		{"bare host defaults to HTTP", "localhost:7890", nil, SourceExplicit, "localhost:7890", "localhost:7890"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := newClient(test.explicit, time.Second, environment(test.env))
			if err != nil {
				t.Fatal(err)
			}
			defer client.HTTP.CloseIdleConnections()
			if client.Source != test.source {
				t.Fatalf("source = %q, want %q", client.Source, test.source)
			}
			transport := client.HTTP.Transport.(*safeTransport).transport
			for scheme, want := range map[string]string{"https": test.httpsHost, "http": test.httpHost} {
				selected, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: scheme, Host: "target.example"}})
				if err != nil {
					t.Fatal(err)
				}
				got := ""
				if selected != nil {
					got = selected.Host
				}
				if got != want {
					t.Errorf("%s proxy = %q, want %q", scheme, got, want)
				}
			}
		})
	}
}

func TestInvalidProxyDoesNotExposeCredentials(t *testing.T) {
	for _, value := range []string{
		"http://private-user:private-password@localhost:invalid",
		"ftp://private-user:private-password@localhost:8080",
		"http://private-user:private-password@localhost:8080/path",
		"http://private-user:private-password@localhost:8080?token=private-token",
		"http://private-user:private-password@%invalid",
		"http://localhost:0", "http://localhost:65536", "http://", " ",
	} {
		client, err := newClient(value, time.Second, environment(nil))
		if err == nil || client != nil {
			t.Fatalf("invalid proxy was accepted")
		}
		for _, secret := range []string{"private-user", "private-password", "private-token"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposed proxy credentials")
			}
		}
	}
	if _, err := newClient("", 0, environment(nil)); err == nil {
		t.Fatal("unbounded timeout was accepted")
	}
}

func TestNoProxyRules(t *testing.T) {
	tests := []struct {
		target string
		rule   string
		match  bool
	}{
		{"https://example.com", "*", true},
		{"https://example.com", "example.com", true},
		{"https://sub.example.com", "example.com", true},
		{"https://notexample.com", "example.com", false},
		{"https://sub.example.com", ".example.com", true},
		{"https://example.com", ".example.com", false},
		{"https://sub.example.com", "*.example.com", true},
		{"https://example.com", "*.example.com", false},
		{"https://EXAMPLE.COM.", "example.com", true},
		{"https://example.com", "example.com:443", true},
		{"https://example.com", "example.com:80", false},
		{"http://example.com", "example.com:80", true},
		{"http://example.com:8080", "example.com:8080", true},
		{"https://192.0.2.12", "192.0.2.0/24", true},
		{"https://192.0.3.12", "192.0.2.0/24", false},
		{"https://example.com", "192.0.2.0/24", false},
		{"https://[2001:db8::1]", "2001:db8::/32", true},
		{"https://[2001:db8::1]", "[2001:db8::1]:443", true},
		{"https://[2001:db8::1]", "[2001:db8::1]:80", false},
		{"https://[::1]", "::1", true},
	}
	for _, test := range tests {
		target, err := url.Parse(test.target)
		if err != nil {
			t.Fatal(err)
		}
		if got := matchesNoProxy(target, []string{test.rule}); got != test.match {
			t.Errorf("target %q, rule %q: match = %t, want %t", test.target, test.rule, got, test.match)
		}
	}
}

func TestNoProxyBlocksInsteadOfConnectingDirectly(t *testing.T) {
	var originHits, proxyHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		fmt.Fprint(w, "origin")
	}))
	defer origin.Close()
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		fmt.Fprint(w, "proxy")
	}))
	defer proxyServer.Close()
	for _, noProxyName := range []string{"NO_PROXY", "no_proxy"} {
		client, err := newClient("", time.Second, environment(map[string]string{"ALL_PROXY": proxyServer.URL, noProxyName: "127.0.0.1"}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.HTTP.Get(origin.URL)
		client.HTTP.CloseIdleConnections()
		if err == nil || !strings.Contains(err.Error(), "NO_PROXY") {
			t.Fatalf("expected an actionable NO_PROXY error")
		}
	}
	if originHits.Load() != 0 || proxyHits.Load() != 0 {
		t.Fatalf("a blocked request contacted the origin or proxy")
	}

	client, err := newClient(proxyServer.URL, time.Second, environment(map[string]string{"NO_PROXY": "*"}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	if got := responseText(t, client, origin.URL); got != "proxy" {
		t.Fatalf("explicit proxy did not override NO_PROXY: %q", got)
	}
	if originHits.Load() != 0 || proxyHits.Load() != 1 {
		t.Fatal("explicit proxy was bypassed")
	}
}

func TestEnvironmentIsSnapshotted(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "first") }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("changed proxy was used") }))
	defer second.Close()
	values := map[string]string{"ALL_PROXY": first.URL}
	client, err := newClient("", time.Second, environment(values))
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	values["ALL_PROXY"] = second.URL
	values["NO_PROXY"] = "*"
	if got := responseText(t, client, "http://target.invalid/"); got != "first" {
		t.Fatalf("request did not retain the original proxy: %q", got)
	}
}

func TestFailedProxyNeverContactsOrigin(t *testing.T) {
	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { originHits.Add(1) }))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := listener.Addr().String()
	listener.Close()
	client, err := newClient("http://private-user:private-password@"+deadAddress, time.Second, environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	_, err = client.HTTP.Get(origin.URL)
	if err == nil {
		t.Fatal("unavailable proxy did not fail")
	}
	if strings.Contains(err.Error(), "private-user") || strings.Contains(err.Error(), "private-password") {
		t.Fatal("network error exposed proxy credentials")
	}
	if originHits.Load() != 0 {
		t.Fatal("proxy failure fell back to a direct connection")
	}
}

func TestHTTPConnectWithVerifiedTLS(t *testing.T) {
	var connects atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached the origin")
		}
		fmt.Fprint(w, "secure origin")
	}))
	defer origin.Close()
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password"))
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != origin.Listener.Addr().String() || r.Header.Get("Proxy-Authorization") != expectedAuth {
			http.Error(w, "unexpected CONNECT request", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		connects.Add(1)
		fmt.Fprint(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
		copied := make(chan struct{})
		go func() {
			io.Copy(upstream, connection)
			upstream.Close()
			close(copied)
		}()
		io.Copy(connection, upstream)
		connection.Close()
		<-copied
	}))
	defer proxyServer.Close()
	proxyURL, _ := url.Parse(proxyServer.URL)
	proxyURL.User = url.UserPassword("proxy-user", "proxy-password")
	client, err := newClient(proxyURL.String(), 2*time.Second, environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	client.HTTP.Transport.(*safeTransport).transport.TLSClientConfig.RootCAs = pool
	if got := responseText(t, client, origin.URL); got != "secure origin" || connects.Load() != 1 {
		t.Fatalf("TLS request did not traverse CONNECT: response=%q, connects=%d", got, connects.Load())
	}
}

func TestUntrustedTLSIsRejected(t *testing.T) {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted request reached handler") }))
	origin.Config.ErrorLog = log.New(io.Discard, "", 0)
	origin.StartTLS()
	defer origin.Close()
	client, err := newClient("", time.Second, environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	if _, err := client.HTTP.Get(origin.URL); err == nil {
		t.Fatal("an untrusted server certificate was accepted")
	}
}

func TestSOCKSUsesRemoteHostname(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverResult := make(chan error, 1)
			go func() { serverResult <- serveSOCKSRequest(listener) }()
			client, err := newClient(scheme+"://"+listener.Addr().String(), 2*time.Second, environment(nil))
			if err != nil {
				t.Fatal(err)
			}
			defer client.HTTP.CloseIdleConnections()
			if got := responseText(t, client, "http://remote-resolution.invalid/"); got != "socks tunnel" {
				t.Fatalf("unexpected SOCKS response: %q", got)
			}
			if err := <-serverResult; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func serveSOCKSRequest(listener net.Listener) error {
	connection, err := listener.Accept()
	if err != nil {
		return err
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(3 * time.Second))
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil {
		return err
	}
	if header[0] != 5 {
		return errors.New("unexpected SOCKS version")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return err
	}
	if _, err := connection.Write([]byte{5, 0}); err != nil {
		return err
	}
	request := make([]byte, 5)
	if _, err := io.ReadFull(connection, request); err != nil {
		return err
	}
	if request[0] != 5 || request[1] != 1 || request[3] != 3 {
		return errors.New("SOCKS request did not pass an unresolved domain to the proxy")
	}
	host := make([]byte, int(request[4])+2)
	if _, err := io.ReadFull(connection, host); err != nil {
		return err
	}
	if string(host[:len(host)-2]) != "remote-resolution.invalid" || host[len(host)-2] != 0 || host[len(host)-1] != 80 {
		return errors.New("unexpected remote SOCKS target")
	}
	if _, err := connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
		return err
	}
	if _, err := http.ReadRequest(bufio.NewReader(connection)); err != nil {
		return err
	}
	_, err = fmt.Fprint(connection, "HTTP/1.1 200 OK\r\nContent-Length: 12\r\nConnection: close\r\n\r\nsocks tunnel")
	return err
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var destinationHits atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationHits.Add(1) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	client, err := newClient("", time.Second, environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	response, err := client.HTTP.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound || destinationHits.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestCancellationRemainsInspectable(t *testing.T) {
	client, err := newClient("", time.Second, environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://target.invalid/", nil)
	_, err = client.HTTP.Do(request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause was lost: %v", err)
	}
}

func responseText(t *testing.T, client *Client, target string) string {
	t.Helper()
	response, err := client.HTTP.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
