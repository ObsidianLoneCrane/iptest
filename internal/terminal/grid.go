package terminal

import "strings"

// Grid renders an arbitrary set of columns using the same sanitizing, wrapping,
// and display-width rules as the source-data table. Rows must match the headers.
func Grid(headers []string, rows [][]string, width int) string {
	width = boundedWidth(width)
	if len(headers) == 0 || len(rows) == 0 || len(headers)*5+1 > width {
		return ""
	}
	cleaned := make([][]string, 0, len(rows)+1)
	cleaned = append(cleaned, headers)
	cleaned = append(cleaned, rows...)
	natural, widths := make([]int, len(headers)), make([]int, len(headers))
	for index, row := range cleaned {
		if len(row) != len(headers) {
			return ""
		}
		copy := make([]string, len(row))
		for column, cell := range row {
			copy[column] = cleanCell(cell)
			natural[column] = max(natural[column], DisplayWidth(copy[column]))
		}
		cleaned[index] = copy
	}
	available := width - 3*len(headers) - 1
	if len(headers) == 2 {
		// Label/value sections keep labels compact and give addresses and other
		// details the remaining width.
		widths[0] = min(max(2, natural[0]), min(24, max(8, available/3)))
		widths[1] = available - widths[0]
		return renderGrid(cleaned, widths)
	}
	for column := range widths {
		widths[column] = 2
		available -= 2
	}
	// Grow columns fairly so one long interface name cannot squeeze the other
	// columns to two cells. Distribute spare space evenly once all values fit.
	for available > 0 {
		grew := false
		for column := range widths {
			if widths[column] < natural[column] && available > 0 {
				widths[column]++
				available--
				grew = true
			}
		}
		if !grew {
			for column := range widths {
				if available > 0 {
					widths[column]++
					available--
				}
			}
		}
	}
	return renderGrid(cleaned, widths)
}

// Section renders a plain-text heading and a width-bounded rule.
func Section(title string, width int) string {
	width = boundedWidth(width)
	return strings.Join(wrapCell(cleanCell(title), width), "\n") + "\n" + strings.Repeat("-", width)
}

// Text wraps and sanitizes a paragraph to the supported terminal width.
func Text(value string, width int) string {
	return strings.Join(wrapCell(cleanCell(value), boundedWidth(width)), "\n")
}
