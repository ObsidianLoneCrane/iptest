//go:build darwin

package traffic

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

func readCounters(ctx context.Context) (map[string]counter, error) {
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, err := exec.CommandContext(bounded, "/usr/sbin/netstat", "-ibn").Output()
	if err != nil {
		return nil, errors.Join(errors.New("could not read macOS interface counters"), bounded.Err())
	}
	return parseDarwin(string(body))
}
