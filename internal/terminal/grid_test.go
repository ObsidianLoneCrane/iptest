package terminal

import (
	"strings"
	"testing"
)

func TestGridWrapsAddressesAndCJKAtNarrowWidthsWithoutLosingValues(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100} {
		rows := [][]string{{"以太网接口", "2001:db8:1234:5678:abcd:ef01:2345:6789", "12.34", "56.78"}}
		grid := Grid([]string{"Interface", "Local IP", "Receive (KiB/s)", "Send (KiB/s)"}, rows, width)
		for _, line := range strings.Split(grid, "\n") {
			if DisplayWidth(line) != width {
				t.Fatalf("width=%d line=%q cells=%d", width, line, DisplayWidth(line))
			}
		}
		// Reconstruct each column across wrapped physical rows to ensure no data
		// disappeared during layout, especially long IPv6 and wide characters.
		var cells [4]string
		for _, line := range strings.Split(grid, "\n") {
			if !strings.HasPrefix(line, "|") {
				continue
			}
			parts := strings.Split(line, "|")
			for column := range cells {
				cells[column] += strings.TrimSpace(parts[column+1])
			}
		}
		for column, value := range rows[0] {
			if !strings.Contains(cells[column], value) {
				t.Fatalf("lost %q at width %d: %q", value, width, grid)
			}
		}
	}
}

func TestGridSanitizesCellsWithoutMutatingInput(t *testing.T) {
	headers := []string{"Target", "State"}
	rows := [][]string{{"\x1b[31mprovider\x1b[0m", "OK\n\x1b]0;injected\a\u202e"}}
	got := Grid(headers, rows, 40)
	if strings.ContainsAny(got, "\x1b\a\u202e") || strings.Contains(got, "injected") || !strings.Contains(got, "provider") || rows[0][1] != "OK\n\x1b]0;injected\a\u202e" {
		t.Fatalf("unsafe output or input mutation: %q", got)
	}
}

func TestGridEmptyAndMismatchedRowsAreOmitted(t *testing.T) {
	if Grid(nil, nil, 40) != "" || Grid([]string{"A"}, nil, 40) != "" || Grid([]string{"A", "B"}, [][]string{{"one"}}, 40) != "" {
		t.Fatal("invalid grid rendered")
	}
}

func TestSectionAndTextRespectWidthAndSanitizeControls(t *testing.T) {
	for _, output := range []string{
		Section(strings.Repeat("IPv6 ", 20)+"\x1b[31m", 40),
		Text(strings.Repeat("本地网卡 2001:db8::1 ", 12)+"\x1b[31m", 40),
	} {
		for _, line := range strings.Split(output, "\n") {
			if DisplayWidth(line) > 40 || strings.Contains(line, "\x1b") {
				t.Fatalf("unsafe or overwide text: %q", line)
			}
		}
	}
}
