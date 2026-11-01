package reputation

import (
	"fmt"
	"strings"
	"testing"
)

func summaryNumber(value int64) *int64 { return &value }
func summaryFlag(value bool) *bool     { return &value }

func TestSummaryRetainsAvailableFactsWhenHistoryIsMissing(t *testing.T) {
	report := Report{
		NetworkType:      "Hosting",
		EstimatedDevices: summaryNumber(0),
		Detections:       Detections{Compromised: summaryFlag(false)},
	}
	if got := tableValues(t, Summary(report)); got != "network=Hosting; compromised=no" {
		t.Fatalf("summary = %q", got)
	}
}

func TestSummaryShowsExplicitFlagsAndPositiveEstimatesWithoutJudgment(t *testing.T) {
	report := Report{
		NetworkType:      "Residential",
		EstimatedDevices: summaryNumber(10000),
		Detections: Detections{
			Proxy: summaryFlag(false), VPN: summaryFlag(true), Tor: summaryFlag(false),
			Hosting: summaryFlag(false), Compromised: summaryFlag(true), Scraper: summaryFlag(true),
		},
		AttackHistory: map[string]int64{"login_attempt": 9, "comment_spam": 2},
	}
	want := "network=Residential; estimated devices=10000; proxy=no; VPN=yes; Tor=no; hosting=no; compromised=yes; scraper=yes; event: comment_spam=2; event: login_attempt=9"
	if got := tableValues(t, Summary(report)); got != want {
		t.Fatalf("summary = %q; want %q", got, want)
	}
}

func TestSummaryOmitsUnavailableAndUnusableFields(t *testing.T) {
	for _, report := range []Report{
		{},
		{EstimatedDevices: summaryNumber(0)},
		{EstimatedDevices: summaryNumber(-1)},
		{NetworkType: " N/A ", AttackHistory: map[string]int64{}},
		{NetworkType: "Unknown", AttackHistory: map[string]int64{"login_attempt": 0, "invalid_count": -1}},
		{NetworkType: "\x1b[31mHosting", AttackHistory: map[string]int64{"bad\nkey": 5, "x; compromised: no": 9}},
		{NetworkType: strings.Repeat("a", 81), AttackHistory: map[string]int64{strings.Repeat("x", 81): 1}},
	} {
		if got := Summary(report); got != "" {
			t.Errorf("unusable fields were displayed: %q", got)
		}
	}
}

func TestSummaryKeepsUnknownEventNamesAsSourceFacts(t *testing.T) {
	report := Report{AttackHistory: map[string]int64{"future_event": 2, "registration_attempt": 3, "form_submission": 4}}
	want := "event: form_submission=4; event: future_event=2; event: registration_attempt=3"
	if got := tableValues(t, Summary(report)); got != want {
		t.Fatalf("summary = %q; want %q", got, want)
	}
}

func TestSummaryBoundsAndSortsEventOutput(t *testing.T) {
	report := Report{AttackHistory: make(map[string]int64)}
	for index := 0; index < 10; index++ {
		report.AttackHistory[fmt.Sprintf("event_%02d", index)] = int64(index + 1)
	}
	got := tableValues(t, Summary(report))
	if !strings.HasPrefix(got, "event: event_00=1; event: event_01=2") || !strings.HasSuffix(got, "event: event_07=8; other event types=2") || strings.Contains(got, "event_08") {
		t.Fatalf("event output is not bounded and deterministic: %q", got)
	}
}

func tableValues(t *testing.T, table string) string {
	t.Helper()
	lines := strings.Split(table, "\n")
	if len(lines) < 3 {
		t.Fatalf("missing table rows: %q", table)
	}
	header := strings.Split(lines[0], "|")
	if len(header) != 5 || strings.TrimSpace(header[1]) != "Field" || strings.TrimSpace(header[2]) != "Status" || strings.TrimSpace(header[3]) != "Meaning" {
		t.Fatalf("unexpected table header: %q", lines[0])
	}
	var values []string
	for _, line := range lines[2:] {
		cells := strings.Split(line, "|")
		if len(cells) != 5 || strings.TrimSpace(cells[3]) == "" {
			t.Fatalf("row needs three columns and an explanation: %q", line)
		}
		values = append(values, strings.TrimSpace(cells[1])+"="+strings.TrimSpace(cells[2]))
	}
	return strings.Join(values, "; ")
}
