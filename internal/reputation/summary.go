package reputation

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/ObsidianLoneCrane/iptest/internal/terminal"
)

const maxDisplayedEvents = 8

// Summary formats usable source fields in a terminal-native table of the default
// width. It assigns no score, risk level, or overall cleanliness judgment.
func Summary(report Report) string {
	return SummaryWidth(report, terminal.DefaultWidth)
}

// SummaryWidth wraps fields to fit the requested terminal width. Empty output
// means that there are no usable source fields to display.
func SummaryWidth(report Report, width int) string {
	var rows []terminal.Row
	if network := sourceLabel(report.NetworkType); network != "" {
		rows = append(rows, terminal.Row{Field: "network", Status: network, Meaning: "Network classification reported by the provider."})
	}
	// Zero is retained in the report but does not provide a useful sharing
	// estimate. Omitting it does not imply zero users or an unused address.
	if report.EstimatedDevices != nil && *report.EstimatedDevices > 0 {
		rows = append(rows, terminal.Row{Field: "estimated devices", Status: fmt.Sprint(*report.EstimatedDevices), Meaning: "Estimated devices behind this IP, not people or concurrent users."})
	}
	for _, flag := range []struct {
		label   string
		value   *bool
		meaning string
	}{
		{"proxy", report.Detections.Proxy, "Whether the provider identifies this IP as a proxy."},
		{"VPN", report.Detections.VPN, "Whether the provider identifies this IP as a VPN exit."},
		{"Tor", report.Detections.Tor, "Whether the provider identifies this IP as a Tor exit."},
		{"hosting", report.Detections.Hosting, "Whether the provider flags a hosting/datacenter address."},
		{"compromised", report.Detections.Compromised, "Whether the provider flags this host as compromised."},
		{"scraper", report.Detections.Scraper, "Whether the provider flags scraper activity from this IP."},
	} {
		if flag.value == nil {
			continue
		}
		value := "no"
		if *flag.value {
			value = "yes"
		}
		rows = append(rows, terminal.Row{Field: flag.label, Status: value, Meaning: flag.meaning})
	}

	var eventKeys []string
	for kind, count := range report.AttackHistory {
		if count > 0 && sourceLabel(kind) != "" {
			eventKeys = append(eventKeys, kind)
		}
	}
	sort.Strings(eventKeys)
	displayed := 0
	for index, kind := range eventKeys {
		if index >= maxDisplayedEvents {
			break
		}
		rows = append(rows, terminal.Row{Field: "event: " + sourceLabel(kind), Status: fmt.Sprint(report.AttackHistory[kind]), Meaning: "Activity count reported by the provider; no local risk judgment."})
		displayed++
	}
	if omitted := len(eventKeys) - displayed; omitted > 0 {
		rows = append(rows, terminal.Row{Field: "other event types", Status: fmt.Sprint(omitted), Meaning: "Additional event types omitted from this table."})
	}
	if len(rows) == 0 {
		return ""
	}
	return terminal.Table(rows, width)
}

// Source labels are short data identifiers, not prose or terminal instructions.
// Preserve readable provider values, including future event names, while
// omitting placeholders, control characters, and output-delimiter injection.
func sourceLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 80 {
		return ""
	}
	switch strings.ToLower(value) {
	case "unknown", "n/a", "na", "none", "null", "undefined", "-":
		return ""
	}
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && !strings.ContainsRune(" _-./", character) {
			return ""
		}
	}
	return value
}
