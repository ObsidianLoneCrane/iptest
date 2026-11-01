package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAutomaticDiscoveryPrecedenceAndRouteIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, explicit string
		env            map[string]string
		want           Source
		calls          int
	}{
		{"explicit", "http://explicit.example:8080", nil, SourceExplicit, 0},
		{"environment", "", map[string]string{"ALL_PROXY": "socks5://env.example:1080"}, SourceEnvironment, 0},
		{"desktop", "", nil, SourceSystemProxy, 1},
		{"HTTP environment does not override HTTPS desktop", "", map[string]string{"HTTP_PROXY": "http://env.example:8080"}, SourceSystemProxy, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client, err := newAutoClient(tc.explicit, time.Second, environment(tc.env), func(time.Duration) (systemSettings, error) {
				calls++
				return systemSettings{HTTPS: "http://desktop.example:7890", Detail: "fixture desktop proxy"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.HTTP.CloseIdleConnections()
			if client.Source != tc.want || calls != tc.calls || client.Detail == "" {
				t.Fatalf("source=%s detail=%q calls=%d", client.Source, client.Detail, calls)
			}
		})
	}
}

func TestAutomaticDiscoveryNoSettingsAndErrors(t *testing.T) {
	client, err := newAutoClient("", time.Second, environment(nil), func(time.Duration) (systemSettings, error) {
		return systemSettings{Warnings: []string{"discovery limited"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	if client.Source != SourceSystemRoute || !strings.Contains(client.Detail, "TUN/VPN") || len(client.Warnings) != 1 {
		t.Fatalf("unlabelled system route: %+v", client)
	}
	for _, expected := range []error{ErrDiscovery, ErrAutoProxyUnsupported} {
		client, err := newAutoClient("", time.Second, environment(nil), func(time.Duration) (systemSettings, error) { return systemSettings{}, expected })
		if client != nil || !errors.Is(err, expected) {
			t.Fatalf("client=%v err=%v", client, err)
		}
	}
	client, err = newAutoClient("", time.Second, environment(nil), func(time.Duration) (systemSettings, error) {
		return systemSettings{HTTPS: "http://private-user:private-password@host:bad"}, nil
	})
	if client != nil || !errors.Is(err, ErrDiscovery) || strings.Contains(err.Error(), "private") {
		t.Fatal("invalid enabled setting was not safely rejected")
	}
}

func TestDiscoveredProxyBypassNeverBecomesDirect(t *testing.T) {
	client, err := newAutoClient("", time.Second, environment(map[string]string{"NO_PROXY": "env.example"}), func(time.Duration) (systemSettings, error) {
		return systemSettings{HTTPS: "http://proxy.example:7890", Bypass: []string{"*.desktop.example", "<local>"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	selectProxy := client.HTTP.Transport.(*safeTransport).transport.Proxy
	for _, target := range []string{"https://env.example", "https://a.desktop.example", "https://printer"} {
		parsed, _ := url.Parse(target)
		if selected, err := selectProxy(&http.Request{URL: parsed}); selected != nil || !errors.Is(err, ErrProxyBypass) {
			t.Fatalf("target=%s proxy=%v error=%v", target, selected, err)
		}
	}
	parsed, _ := url.Parse("https://public.example")
	if selected, err := selectProxy(&http.Request{URL: parsed}); err != nil || selected.Host != "proxy.example:7890" {
		t.Fatalf("proxy=%v error=%v", selected, err)
	}
}

func TestWindowsProxyConfigurationFixtures(t *testing.T) {
	for _, tc := range []struct {
		raw          windowsSettings
		https, socks string
		err          error
		warning      bool
	}{
		{windowsSettings{}, "", "", nil, false},
		{windowsSettings{Proxy: "127.0.0.1:7890"}, "127.0.0.1:7890", "", nil, false},
		{windowsSettings{Proxy: "http=localhost:8080;https=localhost:7890;socks=localhost:1080"}, "localhost:7890", "socks5://localhost:1080", nil, false},
		{windowsSettings{Proxy: "http=localhost:8080"}, "", "", nil, false},
		{windowsSettings{AutoDetect: true}, "", "", nil, true},
		{windowsSettings{Script: "https://private.example/proxy.pac"}, "", "", ErrAutoProxyUnsupported, false},
		{windowsSettings{Proxy: "https="}, "", "", ErrDiscovery, false},
		{windowsSettings{Proxy: "https=a:1;https=b:2"}, "", "", ErrDiscovery, false},
	} {
		settings, err := windowsConfiguration(tc.raw)
		if !errors.Is(err, tc.err) || settings.HTTPS != tc.https || settings.SOCKS != tc.socks || (len(settings.Warnings) > 0) != tc.warning {
			t.Fatalf("settings=%+v error=%v", settings, err)
		}
	}
}

func TestMacProxyConfigurationFixtures(t *testing.T) {
	output := `<dictionary> {
  HTTPSEnable : 1
  HTTPSProxy : localhost
  HTTPSPort : 7890
  SOCKSEnable : 1
  SOCKSProxy : ::1
  SOCKSPort : 1080
  ExceptionsList : <array> {
    0 : *.local
  }
  ExcludeSimpleHostnames : 1
  __SCOPED__ : <dictionary> {
    en1 : <dictionary> {
      HTTPSProxy : wrong-interface.example
      HTTPSPort : 9999
    }
  }
}`
	settings, err := macConfiguration(output)
	if err != nil || settings.HTTPS != "http://localhost:7890" || settings.SOCKS != "socks5://[::1]:1080" || len(settings.Bypass) != 2 {
		t.Fatalf("settings=%+v error=%v", settings, err)
	}
	for _, tc := range []struct {
		input string
		err   error
	}{
		{"garbage", ErrDiscovery},
		{"<dictionary> {\nHTTPSEnable : 1\nHTTPSProxy : localhost\nHTTPSPort : 0\n}", ErrDiscovery},
		{"<dictionary> {\nProxyAutoConfigEnable : 1\n}", ErrAutoProxyUnsupported},
		{"<dictionary> {\nHTTPSEnable : 0\nHTTPSProxy : stale.example\nHTTPSPort : 7890\n}", nil},
		{"<dictionary> {\n}", nil},
	} {
		_, err := macConfiguration(tc.input)
		if !errors.Is(err, tc.err) {
			t.Fatalf("input=%q error=%v", tc.input, err)
		}
	}
}

func gnomeFixture(values map[string]string) commandRunner {
	return func(_ context.Context, name string, args ...string) (string, error) {
		if name != "gsettings" || len(args) != 3 || args[0] != "get" {
			return "", ErrDiscovery
		}
		value, ok := values[args[1]+"."+args[2]]
		if !ok {
			return "", ErrDiscovery
		}
		return value, nil
	}
}

func TestGnomeManualAutoAndDisabledFixtures(t *testing.T) {
	values := map[string]string{
		"org.gnome.system.proxy.mode":         "'manual'",
		"org.gnome.system.proxy.http.host":    "''",
		"org.gnome.system.proxy.https.host":   "'localhost'",
		"org.gnome.system.proxy.https.port":   "7890",
		"org.gnome.system.proxy.socks.host":   "''",
		"org.gnome.system.proxy.ignore-hosts": "['localhost', '127.0.0.0/8', '::1']",
	}
	settings, err := linuxDesktopConfiguration(context.Background(), "ubuntu:gnome", gnomeFixture(values))
	if err != nil || settings.HTTPS != "http://localhost:7890" || len(settings.Bypass) != 3 {
		t.Fatalf("settings=%+v error=%v", settings, err)
	}
	values["org.gnome.system.proxy.mode"] = "'auto'"
	if _, err := gnomeConfiguration(context.Background(), gnomeFixture(values)); !errors.Is(err, ErrAutoProxyUnsupported) {
		t.Fatal(err)
	}
	values["org.gnome.system.proxy.mode"] = "'none'"
	if settings, err := gnomeConfiguration(context.Background(), gnomeFixture(values)); err != nil || settings.HTTPS != "" {
		t.Fatalf("settings=%+v error=%v", settings, err)
	}
	_, err = linuxDesktopConfiguration(context.Background(), "", func(context.Context, string, ...string) (string, error) {
		t.Fatal("headless discovery must not inspect stale desktop settings")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	settings, err = gnomeConfiguration(context.Background(), func(context.Context, string, ...string) (string, error) { return "", exec.ErrNotFound })
	if err != nil || len(settings.Warnings) == 0 {
		t.Fatal("missing gsettings must be explained")
	}
}

func TestKdeConfigurations(t *testing.T) {
	values, err := kdeFileValues("[Other]\nProxyType=2\n[Proxy Settings][$i]\nProxyType[$i]=1\nhttpsProxy=http://localhost 7890\nsocksProxy=socks://localhost 1080\nNoProxyFor=*.local,127.0.0.1\n")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := kdeValues(values)
	if err != nil || settings.HTTPS != "http://localhost:7890" || settings.SOCKS != "socks5://localhost:1080" || len(settings.Bypass) != 2 {
		t.Fatalf("settings=%+v error=%v", settings, err)
	}
	for _, tc := range []struct {
		kind     string
		expected error
	}{{"0", nil}, {"4", ErrAutoProxyUnsupported}, {"2", ErrAutoProxyUnsupported}, {"invalid", ErrDiscovery}} {
		_, err := kdeValues(map[string]string{"ProxyType": tc.kind})
		if !errors.Is(err, tc.expected) {
			t.Fatalf("type=%s error=%v", tc.kind, err)
		}
	}
	if _, err := kdeValues(map[string]string{"ProxyType": "1", "httpsProxy": "http://host:8080", "ReversedException": "true"}); !errors.Is(err, ErrDiscovery) {
		t.Fatal("reversed exceptions cannot be silently ignored")
	}
	if _, err := kdeValues(map[string]string{"ProxyType": "4", "httpsProxy": "CUSTOM_HTTPS_PROXY"}); !errors.Is(err, ErrAutoProxyUnsupported) {
		t.Fatal("unresolved custom environment bindings cannot become system-route selection")
	}
}
