package terminal

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTableWrapsAllColumnsAtNarrowWidths(t *testing.T) {
	rows := []Row{{
		Field:   "event: " + strings.Repeat("long_identifier", 5),
		Status:  "9223372036854775807",
		Meaning: strings.Repeat("A long explanation with readable words. ", 6),
	}}
	for _, width := range []int{40, 60, 80, 100, 240} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			table := Table(rows, width)
			assertAlignedTable(t, table, width)
			if strings.Contains(table, "| -----") || strings.Contains(table, "\t") {
				t.Fatalf("Markdown separators or tabs reached terminal output: %q", table)
			}
			columns := joinedBodyCells(table)
			if strings.ReplaceAll(columns[0], " ", "") != strings.ReplaceAll(rows[0].Field, " ", "") ||
				strings.ReplaceAll(columns[1], " ", "") != rows[0].Status ||
				strings.ReplaceAll(columns[2], " ", "") != strings.ReplaceAll(rows[0].Meaning, " ", "") {
				t.Fatalf("wrapped output lost information: %#v", columns)
			}
		})
	}
}

func TestTableAlignsCJKAndCombiningCharacters(t *testing.T) {
	if got := DisplayWidth("网卡 e\u0301"); got != 6 {
		t.Fatalf("display width = %d; want 6", got)
	}
	rows := []Row{{Field: "中文字段", Status: "启用", Meaning: strings.Repeat("中文说明 cafe\u0301 ", 9)}}
	table := Table(rows, 60)
	assertAlignedTable(t, table, 60)
	if !strings.Contains(table, "cafe\u0301") || strings.Count(table, "中") != 10 {
		t.Fatalf("Unicode content changed while wrapping: %q", table)
	}
}

func TestTableRemovesControlSequencesAndKeepsColumnBoundaries(t *testing.T) {
	rows := []Row{{Field: "\x1b[31mproxy\x1b[0m", Status: "no|yes\t", Meaning: "\x1b]0;private-title\aordinary\ntext\u202e"}}
	table := Table(rows, 80)
	assertAlignedTable(t, table, 80)
	if strings.ContainsAny(table, "\x1b\a\t\u202e") || strings.Contains(table, "private-title") || !strings.Contains(table, "no/yes") || !strings.Contains(table, "ordinary text") {
		t.Fatalf("unsafe source text reached output: %q", table)
	}
}

func TestWriterWidthIgnoresShellWidthForRedirectedOutput(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	if got := WriterWidth(&bytes.Buffer{}); got != DefaultWidth {
		t.Fatalf("buffer width = %d", got)
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "redirected.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got := WriterWidth(file); got != DefaultWidth {
		t.Fatalf("redirected width = %d", got)
	}
}

func TestTableBoundsWidthsAndOmitsEmptyData(t *testing.T) {
	rows := []Row{{Field: "field", Status: "yes", Meaning: strings.Repeat("word ", 100)}}
	for _, testCase := range []struct{ requested, maximum int }{{0, DefaultWidth}, {-1, DefaultWidth}, {1, MinimumWidth}, {10000, MaximumWidth}} {
		assertAlignedTable(t, Table(rows, testCase.requested), testCase.maximum)
	}
	if got := Table(nil, 80); got != "" {
		t.Fatalf("empty table = %q", got)
	}
}

func assertAlignedTable(t *testing.T, table string, maximum int) {
	t.Helper()
	lines := strings.Split(table, "\n")
	if len(lines) < 5 || !strings.HasPrefix(lines[0], "+-") || lines[0] != lines[2] || lines[len(lines)-1] != lines[0] {
		t.Fatalf("missing native table borders: %q", table)
	}
	wantWidth := DisplayWidth(lines[0])
	wantPipes := pipeCells(lines[1])
	for _, line := range lines {
		if width := DisplayWidth(line); width != wantWidth || width > maximum {
			t.Fatalf("misaligned line width=%d; want %d and at most %d: %q", width, wantWidth, maximum, line)
		}
		if strings.HasPrefix(line, "|") && pipeCells(line) != wantPipes {
			t.Fatalf("misaligned column borders in %q", line)
		}
	}
}

func pipeCells(line string) [4]int {
	var positions [4]int
	width, index := 0, 0
	for _, character := range line {
		if character == '|' && index < len(positions) {
			positions[index] = width
			index++
		}
		width += runeWidth(character)
	}
	return positions
}

func joinedBodyCells(table string) [3]string {
	var joined [3]string
	lines := strings.Split(table, "\n")
	for _, line := range lines[3 : len(lines)-1] {
		cells := strings.Split(line, "|")
		for column := range joined {
			joined[column] += strings.TrimSpace(cells[column+1])
		}
	}
	return joined
}
