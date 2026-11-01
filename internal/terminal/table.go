// Package terminal renders plain-text output without terminal control sequences.
package terminal

import (
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
)

const (
	DefaultWidth = 100
	MinimumWidth = 40
	MaximumWidth = 240
)

// Row is one source field and its explanatory text.
type Row struct {
	Field   string
	Status  string
	Meaning string
}

// WriterWidth uses the actual console viewport when available. Redirected files
// and non-file writers receive deterministic output independent of shell state.
func WriterWidth(writer io.Writer) int {
	file, ok := writer.(*os.File)
	if !ok {
		return DefaultWidth
	}
	width, console := consoleWidth(file)
	if !console {
		return DefaultWidth
	}
	if width <= 0 {
		if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns >= MinimumWidth && columns <= MaximumWidth {
			width = columns
		}
	}
	return boundedWidth(width)
}

// Table renders three columns with ASCII borders. Long cells wrap without
// truncating source facts. Its supported width range is 40 to 240 cells.
func Table(rows []Row, width int) string {
	if len(rows) == 0 {
		return ""
	}
	width = boundedWidth(width)
	cleaned := make([][]string, 0, len(rows)+1)
	cleaned = append(cleaned, []string{"Field", "Status", "Meaning"})
	var natural [3]int
	for _, row := range rows {
		cleaned = append(cleaned, []string{cleanCell(row.Field), cleanCell(row.Status), cleanCell(row.Meaning)})
	}
	for _, row := range cleaned {
		for column, cell := range row {
			natural[column] = max(natural[column], DisplayWidth(cell))
		}
	}
	available := width - 10 // Four borders and two spaces per column.
	widths := [3]int{
		min(natural[0], min(24, max(8, available/3))),
		min(natural[1], min(20, max(6, available/4))),
		0,
	}
	widths[2] = available - widths[0] - widths[1]
	return renderGrid(cleaned, widths[:])
}

func renderGrid(cleaned [][]string, widths []int) string {
	var border strings.Builder
	border.WriteByte('+')
	for _, size := range widths {
		border.WriteString(strings.Repeat("-", size+2))
		border.WriteByte('+')
	}
	line := border.String()
	var output strings.Builder
	output.WriteString(line)
	for index, row := range cleaned {
		cells := make([][]string, len(widths))
		height := 1
		for column, cell := range row {
			cells[column] = wrapCell(cell, widths[column])
			height = max(height, len(cells[column]))
		}
		for continuation := 0; continuation < height; continuation++ {
			output.WriteString("\n|")
			for column := range widths {
				text := ""
				if continuation < len(cells[column]) {
					text = cells[column][continuation]
				}
				output.WriteByte(' ')
				output.WriteString(text)
				output.WriteString(strings.Repeat(" ", widths[column]-DisplayWidth(text)+1))
				output.WriteByte('|')
			}
		}
		if index == 0 {
			output.WriteByte('\n')
			output.WriteString(line)
		}
	}
	output.WriteByte('\n')
	output.WriteString(line)
	return output.String()
}

func boundedWidth(width int) int {
	if width <= 0 {
		return DefaultWidth
	}
	return min(MaximumWidth, max(MinimumWidth, width))
}

func wrapCell(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && DisplayWidth(line)+1+DisplayWidth(word) <= width {
			line += " " + word
			continue
		}
		if line != "" {
			lines = append(lines, line)
			line = ""
		}
		for _, character := range word {
			if line != "" && DisplayWidth(line)+runeWidth(character) > width {
				lines = append(lines, line)
				line = ""
			}
			line += string(character)
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

// cleanCell strips ANSI CSI/OSC sequences and controls before layout. Newlines
// and tabs become ordinary whitespace; bidi format controls are not emitted.
func cleanCell(text string) string {
	runes := []rune(text)
	var output strings.Builder
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		if character == '\x1b' {
			if index+1 < len(runes) && runes[index+1] == '[' {
				index += 2
				for index < len(runes) && (runes[index] < 0x40 || runes[index] > 0x7e) {
					index++
				}
			} else if index+1 < len(runes) && runes[index+1] == ']' {
				index += 2
				for index < len(runes) && runes[index] != '\a' {
					if runes[index] == '\x1b' && index+1 < len(runes) && runes[index+1] == '\\' {
						index++
						break
					}
					index++
				}
			} else if index+1 < len(runes) {
				index++
			}
			continue
		}
		if unicode.IsSpace(character) {
			output.WriteByte(' ')
		} else if !unicode.IsControl(character) && !unicode.Is(unicode.Cf, character) {
			if character == '|' {
				character = '/'
			}
			output.WriteRune(character)
		}
	}
	return strings.Join(strings.Fields(output.String()), " ")
}
