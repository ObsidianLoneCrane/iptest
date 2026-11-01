package traffic

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

func parseLinux(body string) (map[string]counter, error) {
	result := make(map[string]counter)
	for _, line := range strings.Split(body, "\n") {
		name, values, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(values)
		if len(fields) < 16 {
			return nil, errors.New("invalid Linux interface counters")
		}
		receive, e1 := strconv.ParseUint(fields[0], 10, 64)
		send, e2 := strconv.ParseUint(fields[8], 10, 64)
		name = strings.TrimSpace(name)
		if e1 != nil || e2 != nil || name == "" {
			return nil, errors.New("invalid Linux interface counters")
		}
		result[name] = counter{receive, send}
	}
	if len(result) == 0 {
		return nil, errors.New("no Linux interface counters found")
	}
	return result, nil
}

func parseWindows(body []byte) (map[string]counter, error) {
	// Windows PowerShell emits a singleton object for one adapter and an array
	// for multiple adapters. Null is not a valid zero-byte counter.
	type item struct {
		Name          string
		ReceivedBytes *uint64
		SentBytes     *uint64
	}
	var entries []item
	body = []byte(strings.TrimPrefix(strings.TrimSpace(string(body)), "\ufeff"))
	if len(body) > 0 && body[0] == '{' {
		var entry item
		if json.Unmarshal(body, &entry) != nil {
			return nil, errors.New("invalid Windows interface counters")
		}
		entries = []item{entry}
	} else if json.Unmarshal(body, &entries) != nil {
		return nil, errors.New("invalid Windows interface counters")
	}
	result := make(map[string]counter)
	for _, entry := range entries {
		if entry.Name == "" || entry.ReceivedBytes == nil || entry.SentBytes == nil {
			return nil, errors.New("invalid Windows interface counters")
		}
		result[entry.Name] = counter{*entry.ReceivedBytes, *entry.SentBytes}
	}
	if len(result) == 0 {
		return nil, errors.New("no Windows interface counters found")
	}
	return result, nil
}

func parseDarwin(body string) (map[string]counter, error) {
	result := make(map[string]counter)
	var header []string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "Name" {
			header = fields
			continue
		}
		if len(header) == 0 {
			continue
		}
		index := func(name string) int {
			for i, value := range header {
				if value == name {
					return i
				}
			}
			return -1
		}
		network, ibytes, obytes := index("Network"), index("Ibytes"), index("Obytes")
		if network < 0 || ibytes < 0 || obytes < 0 {
			return nil, errors.New("invalid macOS interface counter header")
		}
		if network >= len(fields) || !strings.HasPrefix(fields[network], "<Link#") {
			continue
		}
		// Point-to-point and loopback link rows have no hardware address;
		// netstat leaves that column blank, which strings.Fields removes.
		address := index("Address")
		if address == network+1 && address < len(fields) {
			if _, err := strconv.ParseUint(fields[address], 10, 64); err == nil {
				ibytes--
				obytes--
			}
		}
		if ibytes >= len(fields) || obytes >= len(fields) {
			return nil, errors.New("invalid macOS interface counters")
		}
		receive, e1 := strconv.ParseUint(fields[ibytes], 10, 64)
		send, e2 := strconv.ParseUint(fields[obytes], 10, 64)
		if e1 != nil || e2 != nil {
			return nil, errors.New("invalid macOS interface counters")
		}
		result[strings.TrimSuffix(fields[0], "*")] = counter{receive, send}
	}
	if len(result) == 0 {
		return nil, errors.New("no macOS link counters found")
	}
	return result, nil
}
