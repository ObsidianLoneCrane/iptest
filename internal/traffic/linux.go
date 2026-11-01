//go:build linux

package traffic

import (
	"context"
	"errors"
	"os"
)

func readCounters(ctx context.Context) (map[string]counter, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return nil, errors.New("could not read Linux interface counters")
	}
	return parseLinux(string(body))
}
