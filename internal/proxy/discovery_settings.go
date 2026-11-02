package proxy

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type windowsSettings struct {
	AutoDetect            bool
	Script, Proxy, Bypass string
}

func windowsConfiguration(raw windowsSettings) (systemSettings, error) {
	if strings.TrimSpace(raw.Script) != "" {
		return systemSettings{}, ErrAutoProxyUnsupported
	}
	settings := systemSettings{Detail: "Windows current-user system proxy", Bypass: splitBypass(raw.Bypass)}
	value := strings.TrimSpace(raw.Proxy)
	if value != "" {
		if !strings.Contains(value, "=") {
			settings.HTTP, settings.HTTPS = value, value
		} else {
			seen := make(map[string]bool)
			for _, entry := range strings.Split(value, ";") {
				kind, address, ok := strings.Cut(entry, "=")
				if !ok || strings.TrimSpace(address) == "" {
					return systemSettings{}, ErrDiscovery
				}
				kind = strings.ToLower(strings.TrimSpace(kind))
				if seen[kind] {
					return systemSettings{}, ErrDiscovery
				}
				seen[kind] = true
				switch kind {
				case "http":
					settings.HTTP = strings.TrimSpace(address)
				case "https":
					settings.HTTPS = strings.TrimSpace(address)
				case "socks":
					settings.SOCKS = socksAddress(address)
				}
			}
		}
	}
	if raw.AutoDetect && settings.HTTPS == "" && settings.SOCKS == "" {
		settings.Warnings = []string{"Windows automatic proxy discovery is enabled but not resolved; using the current system route, which may include TUN/VPN. An automatic proxy may still exist."}
	}
	return settings, nil
}

func splitBypass(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool { return character == ';' || character == ',' })
}

func socksAddress(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "socks://")
	if !strings.Contains(value, "://") {
		value = "socks5://" + value
	}
	return value
}

func macConfiguration(output string) (systemSettings, error) {
	values := make(map[string]string)
	var bypass []string
	inExceptions := false
	depth := 0
	scoped := false
	sawRoot := false
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "}" {
			if depth == 2 && inExceptions {
				inExceptions = false
			}
			depth--
			if depth < 0 {
				return systemSettings{}, ErrDiscovery
			}
			continue
		}
		if strings.HasSuffix(line, "{") {
			if depth == 0 {
				if sawRoot || line != "<dictionary> {" {
					return systemSettings{}, ErrDiscovery
				}
				sawRoot = true
			}
			if depth == 1 && strings.HasPrefix(line, "ExceptionsList :") {
				inExceptions = true
			}
			if depth == 1 && strings.HasPrefix(line, "__SCOPED__ :") {
				scoped = true
			}
			depth++
			continue
		}
		if inExceptions && depth == 2 {
			if _, entry, ok := strings.Cut(line, " : "); ok {
				bypass = append(bypass, entry)
			}
			continue
		}
		if depth != 1 {
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if ok {
			values[key] = value
		}
	}
	if scanner.Err() != nil || depth != 0 || !sawRoot {
		return systemSettings{}, ErrDiscovery
	}
	for _, key := range []string{"ProxyAutoConfigEnable", "ProxyAutoDiscoveryEnable", "ExcludeSimpleHostnames"} {
		if values[key] != "" && values[key] != "0" && values[key] != "1" {
			return systemSettings{}, ErrDiscovery
		}
	}
	if values["ProxyAutoConfigEnable"] == "1" {
		return systemSettings{}, ErrAutoProxyUnsupported
	}
	settings := systemSettings{Detail: "macOS system proxy", Bypass: bypass}
	for _, kind := range []string{"HTTP", "HTTPS", "SOCKS"} {
		enabled := values[kind+"Enable"]
		if enabled == "" || enabled == "0" {
			continue
		}
		if enabled != "1" {
			return systemSettings{}, ErrDiscovery
		}
		address, err := proxyAddress(values[kind+"Proxy"], values[kind+"Port"], kind == "SOCKS")
		if err != nil {
			return systemSettings{}, err
		}
		switch kind {
		case "HTTP":
			settings.HTTP = address
		case "HTTPS":
			settings.HTTPS = address
		case "SOCKS":
			settings.SOCKS = address
		}
	}
	if values["ExcludeSimpleHostnames"] == "1" {
		settings.Bypass = append(settings.Bypass, "<local>")
	}
	if values["ProxyAutoDiscoveryEnable"] == "1" && settings.HTTPS == "" && settings.SOCKS == "" {
		settings.Warnings = []string{"macOS automatic proxy discovery is enabled but not resolved; using the current system route, which may include TUN/VPN. An automatic proxy may still exist."}
	}
	if scoped && settings.HTTPS == "" && settings.SOCKS == "" {
		settings.Warnings = append(settings.Warnings, "macOS per-interface proxy settings are present but no global HTTPS proxy was selected; using the current system route.")
	}
	return settings, nil
}

func proxyAddress(host, port string, socks bool) (string, error) {
	host = strings.TrimSpace(host)
	number, err := strconv.Atoi(strings.TrimSpace(port))
	if host == "" || err != nil || number < 1 || number > 65535 || strings.ContainsAny(host, " \t\r\n/@?#") {
		return "", ErrDiscovery
	}
	scheme := "http://"
	if socks {
		scheme = "socks5://"
	}
	return scheme + net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(number)), nil
}

func linuxConfiguration(ctx context.Context, run commandRunner) (systemSettings, error) {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	if desktop == "" {
		desktop = strings.ToLower(os.Getenv("DESKTOP_SESSION"))
	}
	return linuxDesktopConfiguration(ctx, desktop, run)
}

func linuxDesktopConfiguration(ctx context.Context, desktop string, run commandRunner) (systemSettings, error) {
	if strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity") {
		return gnomeConfiguration(ctx, run)
	}
	if strings.Contains(desktop, "kde") || strings.Contains(desktop, "plasma") {
		return kdeConfiguration(ctx, run)
	}
	settings := systemSettings{}
	if strings.TrimSpace(desktop) != "" {
		settings.Warnings = []string{"Desktop proxy discovery is unavailable for this desktop session; using the current system route, which may include TUN/VPN."}
	}
	return settings, nil
}

func gnomeConfiguration(ctx context.Context, run commandRunner) (systemSettings, error) {
	get := func(schema, key string) (string, error) {
		value, err := run(ctx, "gsettings", "get", schema, key)
		return strings.Trim(strings.TrimSpace(value), "'\""), err
	}
	mode, err := get("org.gnome.system.proxy", "mode")
	if errors.Is(err, exec.ErrNotFound) {
		return systemSettings{Warnings: []string{"GNOME proxy settings could not be inspected because gsettings is unavailable; using the current system route."}}, nil
	}
	if err != nil {
		return systemSettings{}, ErrDiscovery
	}
	switch mode {
	case "none":
		return systemSettings{}, nil
	case "auto":
		return systemSettings{}, ErrAutoProxyUnsupported
	case "manual":
	default:
		return systemSettings{}, ErrDiscovery
	}
	settings := systemSettings{Detail: "GNOME system proxy"}
	for _, kind := range []string{"http", "https", "socks"} {
		host, err := get("org.gnome.system.proxy."+kind, "host")
		if err != nil {
			return systemSettings{}, ErrDiscovery
		}
		if host == "" {
			continue
		}
		port, err := get("org.gnome.system.proxy."+kind, "port")
		if err != nil {
			return systemSettings{}, ErrDiscovery
		}
		address, err := proxyAddress(host, port, kind == "socks")
		if err != nil {
			return systemSettings{}, err
		}
		switch kind {
		case "http":
			settings.HTTP = address
		case "https":
			settings.HTTPS = address
		case "socks":
			settings.SOCKS = address
		}
	}
	if settings.HTTPS == "" && settings.HTTP != "" {
		if same, err := get("org.gnome.system.proxy", "use-same-proxy"); err == nil && same == "true" {
			settings.HTTPS = settings.HTTP
		}
	}
	ignore, err := get("org.gnome.system.proxy", "ignore-hosts")
	if err != nil {
		return systemSettings{}, ErrDiscovery
	}
	for _, value := range strings.Split(strings.Trim(ignore, "[]"), ",") {
		value = strings.Trim(strings.TrimSpace(value), "'\"")
		if value != "" {
			settings.Bypass = append(settings.Bypass, value)
		}
	}
	if settings.HTTP == "" && settings.HTTPS == "" && settings.SOCKS == "" {
		return systemSettings{}, ErrDiscovery
	}
	return settings, nil
}

func kdeConfiguration(ctx context.Context, run commandRunner) (systemSettings, error) {
	var tool string
	for _, candidate := range []string{"kreadconfig6", "kreadconfig5"} {
		_, err := run(ctx, candidate, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "--default", "0")
		if err == nil {
			tool = candidate
			break
		}
		if !errors.Is(err, exec.ErrNotFound) {
			return systemSettings{}, ErrDiscovery
		}
	}
	if tool == "" {
		// Minimal KDE installations may omit the reader. Read the user's file,
		// but report this reduced coverage instead of guessing at defaults.
		path, err := os.UserConfigDir()
		if err != nil || !filepath.IsAbs(path) {
			return systemSettings{}, ErrDiscovery
		}
		data, err := os.ReadFile(filepath.Join(path, "kioslaverc"))
		if errors.Is(err, os.ErrNotExist) {
			return systemSettings{Warnings: []string{"KDE settings readers and a user proxy configuration are unavailable; using the current system route."}}, nil
		}
		if err != nil || len(data) > 64*1024 {
			return systemSettings{}, ErrDiscovery
		}
		values, err := kdeFileValues(string(data))
		if err != nil {
			return systemSettings{}, err
		}
		settings, err := kdeValues(values)
		settings.Warnings = append(settings.Warnings, "KDE proxy settings were read from the user file only; system-wide KConfig defaults were not inspected.")
		return settings, err
	}
	values := make(map[string]string)
	for _, key := range []string{"ProxyType", "httpProxy", "httpsProxy", "socksProxy", "NoProxyFor", "ReversedException"} {
		value, err := run(ctx, tool, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key, "--default", "")
		if err != nil {
			return systemSettings{}, ErrDiscovery
		}
		values[key] = strings.TrimSpace(value)
	}
	return kdeValues(values)
}

func kdeFileValues(data string) (map[string]string, error) {
	values := make(map[string]string)
	active := false
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			group, _, _ := strings.Cut(line, "]")
			active = group == "[Proxy Settings"
			continue
		}
		if !active || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			key, _, _ = strings.Cut(strings.TrimSpace(key), "[")
			values[key] = strings.TrimSpace(value)
		}
	}
	if scanner.Err() != nil {
		return nil, ErrDiscovery
	}
	return values, nil
}

func kdeValues(values map[string]string) (systemSettings, error) {
	settings := systemSettings{Detail: "KDE system proxy", Bypass: splitBypass(values["NoProxyFor"])}
	switch values["ProxyType"] {
	case "", "0":
		return systemSettings{}, nil
	case "2", "4":
		// KDE's environment mode may refer to custom variable names in the
		// protocol fields. Standard proxy variables were already considered;
		// unresolved custom bindings must not be treated as disabled proxies.
		return systemSettings{}, ErrAutoProxyUnsupported
	case "3":
		return systemSettings{Warnings: []string{"KDE automatic proxy discovery is enabled but not resolved; using the current system route, which may include TUN/VPN. An automatic proxy may still exist."}}, nil
	case "1":
	default:
		return systemSettings{}, ErrDiscovery
	}
	if strings.EqualFold(values["ReversedException"], "true") {
		return systemSettings{}, ErrDiscovery
	}
	for _, entry := range []struct {
		key    string
		target *string
		socks  bool
	}{
		{"httpProxy", &settings.HTTP, false}, {"httpsProxy", &settings.HTTPS, false}, {"socksProxy", &settings.SOCKS, true},
	} {
		value := strings.TrimSpace(values[entry.key])
		if value == "" {
			continue
		}
		parts := strings.Fields(value)
		if len(parts) == 2 {
			value = parts[0] + ":" + parts[1]
		} else if len(parts) != 1 {
			return systemSettings{}, ErrDiscovery
		}
		if entry.socks {
			value = socksAddress(value)
		}
		*entry.target = value
	}
	if settings.HTTP == "" && settings.HTTPS == "" && settings.SOCKS == "" {
		return systemSettings{}, ErrDiscovery
	}
	return settings, nil
}
