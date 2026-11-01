package reputation

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode"
)

const maxDisplayedEvents = 8

// Summary formats usable source fields as a Field/Status/Meaning table. It does not assign a score,
// establish risk, infer user counts, or require every optional field to exist.
// Empty output means that there are no usable fields to display.
func Summary(report Report) string {
	type row struct{ field, status, meaning string }
	var rows []row
	if network := sourceLabel(report.NetworkType); network != "" {
		rows = append(rows, row{"network", network, "Network classification reported by the provider."})
	}
	// Zero is retained in the report but does not provide a useful sharing
	// estimate. Omitting it does not imply zero users or an unused address.
	if report.EstimatedDevices != nil && *report.EstimatedDevices > 0 {
		rows = append(rows, row{"estimated devices", fmt.Sprint(*report.EstimatedDevices), "Estimated devices behind this IP, not people or concurrent users."})
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
		rows = append(rows, row{flag.label, value, flag.meaning})
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
		rows = append(rows, row{"event: " + sourceLabel(kind), fmt.Sprint(report.AttackHistory[kind]), "Activity count reported by the provider; no local risk judgment."})
		displayed++
	}
	if omitted := len(eventKeys) - displayed; omitted > 0 {
		rows = append(rows, row{"other event types", fmt.Sprint(omitted), "Additional event types omitted from this table."})
	}
	if len(rows) == 0 {
		return ""
	}
	var table strings.Builder
	writer := tabwriter.NewWriter(&table, 0, 4, 1, ' ', 0)
	fmt.Fprintln(writer, "| Field\t| Status\t| Meaning |")
	fmt.Fprintln(writer, "| -----\t| ------\t| ------- |")
	for _, row := range rows {
		fmt.Fprintf(writer, "| %s\t| %s\t| %s |\n", row.field, row.status, row.meaning)
	}
	writer.Flush()
	return strings.TrimSuffix(table.String(), "\n")
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
