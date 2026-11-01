package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ObsidianLoneCrane/iptest/internal/netinfo"
	"github.com/ObsidianLoneCrane/iptest/internal/reputation"
	"github.com/ObsidianLoneCrane/iptest/internal/speedtest"
	"github.com/ObsidianLoneCrane/iptest/internal/terminal"
	"github.com/ObsidianLoneCrane/iptest/internal/traffic"
)

// output keeps presentation independent of inspection. Report output is buffered
// so progress/diagnostics cannot split its tables; cancellation still flushes
// completed sections. Plain output retains the original line-oriented format.
type output struct {
	writer io.Writer
	plain  bool
	width  int
	buffer strings.Builder
}

func newOutput(writer io.Writer, format string) *output {
	width := terminal.WriterWidth(writer)
	if format == "report" {
		width = min(100, width)
	}
	return &output{writer: writer, plain: format == "plain", width: width}
}

func (view *output) flush() {
	if !view.plain {
		_, _ = io.WriteString(view.writer, view.buffer.String())
	}
}

func (view *output) section(title, body string) {
	if view.buffer.Len() == 0 {
		view.buffer.WriteString("iptest / Network inspection\n\n")
	} else {
		view.buffer.WriteByte('\n')
	}
	view.buffer.WriteString(terminal.Section(title, view.width))
	view.buffer.WriteByte('\n')
	view.buffer.WriteString(body)
	view.buffer.WriteByte('\n')
}

func (view *output) connection(path string) {
	if view.plain {
		fmt.Fprintf(view.writer, "Network path: %s\n", path)
		return
	}
	view.section("Connection", terminal.Grid([]string{"Setting", "Value"}, [][]string{{"Network path", path}}, view.width))
}

func (view *output) local(addresses []netinfo.Address, rates []traffic.Rate, sampled, failed bool) {
	if view.plain {
		if len(addresses) == 0 {
			fmt.Fprintln(view.writer, "Local interface IP: Unknown (no usable address in requested families)")
		}
		for _, address := range addresses {
			fmt.Fprintf(view.writer, "Local interface IP: %s (%s)\n", address.IP, terminalText(address.Interface))
		}
		for _, rate := range rates {
			fmt.Fprintf(view.writer, "Interface traffic (%s): receive %.2f KiB/s; send %.2f KiB/s\n", terminalText(rate.Interface), rate.ReceiveBytesPerSecond/1024, rate.SendBytesPerSecond/1024)
		}
		return
	}
	var rows [][]string
	seen := make(map[string]bool)
	byInterface := make(map[string]traffic.Rate)
	for _, rate := range rates {
		byInterface[rate.Interface] = rate
	}
	makeRow := func(name, address string, first bool) []string {
		row := []string{name, address}
		if sampled {
			receive, send := "-", "-"
			if rate, ok := byInterface[name]; ok && first {
				receive, send = fmt.Sprintf("%.2f", rate.ReceiveBytesPerSecond/1024), fmt.Sprintf("%.2f", rate.SendBytesPerSecond/1024)
			}
			row = append(row, receive, send)
		}
		return row
	}
	for _, address := range addresses {
		rows = append(rows, makeRow(address.Interface, address.IP.String(), !seen[address.Interface]))
		seen[address.Interface] = true
	}
	// Interfaces with counters but no address in the requested family still
	// provide useful traffic data. Never sum their physical/tunnel counters.
	for _, rate := range rates {
		if !seen[rate.Interface] {
			rows = append(rows, makeRow(rate.Interface, "-", true))
			seen[rate.Interface] = true
		}
	}
	headers := []string{"Interface", "Local IP"}
	if sampled {
		headers = append(headers, "Receive (KiB/s)", "Send (KiB/s)")
	}
	body := terminal.Grid(headers, rows, view.width)
	if len(addresses) == 0 {
		body += "\n" + terminal.Text("No usable local address in the requested families.", view.width)
	}
	if sampled {
		body += "\n" + terminal.Text("Traffic includes other applications; rates are shown once per interface. '-' means not sampled or not applicable.", view.width)
		if failed {
			body += "\n" + terminal.Text("Interface traffic sample unavailable; other results are retained.", view.width)
		}
	}
	view.section("Local network", strings.TrimSpace(body))
}

func (view *output) egress(family, ip, location string) {
	if view.plain {
		fmt.Fprintf(view.writer, "Public egress IP (%s): %s\nEstimated location (%s): %s\n", family, ip, family, location)
		return
	}
	view.section("Public egress / "+family, terminal.Grid([]string{"Field", "Value"}, [][]string{{"Public IP", ip}, {"Location", location}}, view.width))
}

func (view *output) source(family string, report reputation.Report) {
	summary := reputation.SummaryWidth(report, view.width)
	if summary == "" {
		return
	}
	if view.plain {
		fmt.Fprintf(view.writer, "IP source data (%s, proxycheck.io):\n%s\n", family, summary)
		return
	}
	view.section("IP source data / "+family+" / proxycheck.io", summary)
}

func (view *output) speed(report speedtest.Report, compare bool, stderr io.Writer) {
	if view.plain {
		printSpeedReport(report, compare, view.writer, stderr)
		return
	}
	var rows [][]string
	var samples []string
	for _, attempt := range report.Attempts {
		result := attempt.Result
		if attempt.Err != nil {
			fmt.Fprintf(stderr, "iptest: speed target %s: %s\n", terminalText(result.Target), speedDiagnostic(attempt.Err))
		}
		if !compare && !result.LatencyMeasured && !result.DownloadMeasured {
			continue
		}
		download, latency, status := "-", "-", "OK"
		if result.DownloadMeasured {
			download = fmt.Sprintf("%.2f", result.DownloadMbps)
			samples = append(samples, fmt.Sprintf("%s: %.2f MiB in %.2f s.", result.Target, float64(result.Bytes)/(1024*1024), result.Duration.Seconds()))
		}
		if result.LatencyMeasured {
			latency = fmt.Sprintf("%.1f", float64(result.Latency)/float64(time.Millisecond))
		}
		if attempt.Skipped {
			status = "Skipped"
		} else if attempt.Err != nil {
			status = "Failed"
		}
		rows = append(rows, []string{result.Target, download, latency, status})
	}
	body := terminal.Grid([]string{"Target", "Download (Mbps)", "HTTP (ms)", "Status"}, rows, view.width)
	if len(rows) == 0 {
		body = "No valid measurement; see diagnostics."
	}
	for _, sample := range samples {
		body += "\n" + terminal.Text(sample, view.width)
	}
	if len(report.Attempts) > 0 {
		body += "\n" + terminal.Text(fmt.Sprintf("Total payload read: %.2f MiB in %.2f s, including failed attempts.", float64(report.Bytes)/(1024*1024), report.Duration.Seconds()), view.width)
	}
	body += "\n" + terminal.Text("Target-specific download estimate; HTTP response latency is not ICMP ping. '-' means not measured.", view.width)
	mode := "automatic failover"
	if compare {
		mode = "sequential comparison"
	}
	view.section("Network speed / "+mode, body)
}
