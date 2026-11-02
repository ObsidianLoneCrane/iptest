package proxy

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var (
	ErrAutoProxyUnsupported = errors.New("automatic proxy scripts are not supported; select an explicit proxy with --proxy")
	ErrDiscovery            = errors.New("could not safely read or use system proxy settings")
)

type systemSettings struct {
	HTTP, HTTPS, SOCKS string
	Bypass             []string
	Detail             string
	Warnings           []string
}

func routeDetail(source Source) string {
	switch source {
	case SourceExplicit:
		return "explicit proxy"
	case SourceEnvironment:
		return "proxy environment variables"
	default:
		return "current system route (may include TUN/VPN)"
	}
}

func newAutoClient(explicit string, timeout time.Duration, env func(string) (string, bool), discover func(time.Duration) (systemSettings, error)) (*Client, error) {
	client, err := newClient(explicit, timeout, env)
	if err != nil || client.Source != SourceSystemRoute {
		return client, err
	}
	settings, err := discover(timeout)
	if err != nil {
		client.HTTP.CloseIdleConnections()
		return nil, err
	}
	client.Warnings = settings.Warnings
	if settings.HTTPS == "" && settings.SOCKS == "" {
		return client, nil
	}
	var httpsProxy, httpProxy *url.URL
	for _, entry := range []struct {
		raw    string
		target **url.URL
	}{
		{settings.HTTPS, &httpsProxy}, {settings.HTTP, &httpProxy},
	} {
		raw := entry.raw
		if raw == "" {
			raw = settings.SOCKS
		}
		if raw == "" {
			continue
		}
		parsed, parseErr := parseProxy(raw)
		if parseErr != nil {
			client.HTTP.CloseIdleConnections()
			return nil, ErrDiscovery
		}
		*entry.target = parsed
	}
	// NO_PROXY remains fail-closed when desktop discovery selects the proxy.
	bypass := append(append([]string{}, settings.Bypass...), strings.Split(envValue(env, "NO_PROXY", "no_proxy"), ",")...)
	transport := client.HTTP.Transport.(*safeTransport).transport
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		selected := httpsProxy
		if request.URL.Scheme == "http" {
			selected = httpProxy
		}
		if selected != nil && (matchesNoProxy(request.URL, bypass) || matchesSystemBypass(request.URL, bypass)) {
			return nil, ErrProxyBypass
		}
		return selected, nil
	}
	client.Source = SourceSystemProxy
	client.Detail = settings.Detail
	if client.Detail == "" {
		client.Detail = "enabled system proxy"
	}
	return client, nil
}

func matchesSystemBypass(target *url.URL, bypass []string) bool {
	for _, rule := range bypass {
		if strings.EqualFold(strings.TrimSpace(rule), "<local>") && !strings.Contains(target.Hostname(), ".") && !strings.Contains(target.Hostname(), ":") {
			return true
		}
	}
	return false
}

func discoverSystem(timeout time.Duration) (systemSettings, error) {
	if timeout > 3*time.Second {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	switch runtime.GOOS {
	case "windows":
		settings, err := readWindowsSettings()
		if err != nil {
			return systemSettings{}, ErrDiscovery
		}
		return windowsConfiguration(settings)
	case "darwin":
		output, err := runDiscoveryCommand(ctx, "scutil", "--proxy")
		if err != nil {
			return systemSettings{}, ErrDiscovery
		}
		return macConfiguration(output)
	case "linux":
		return linuxConfiguration(ctx, runDiscoveryCommand)
	default:
		return systemSettings{Warnings: []string{"Desktop proxy discovery is unavailable on this operating system; using the current system route."}}, nil
	}
}

type commandRunner func(context.Context, string, ...string) (string, error)

func runDiscoveryCommand(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	hideDiscoveryWindow(command)
	var output limitedCommandOutput
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", err
	}
	return output.String(), nil
}

type limitedCommandOutput struct{ bytes.Buffer }

func (output *limitedCommandOutput) Write(value []byte) (int, error) {
	if output.Len()+len(value) > 64*1024 {
		return 0, ErrDiscovery
	}
	return output.Buffer.Write(value)
}
