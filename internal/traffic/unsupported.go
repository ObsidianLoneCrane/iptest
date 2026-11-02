//go:build !linux && !windows && !darwin

package traffic

import (
	"context"
	"errors"
)

func readCounters(context.Context) (map[string]counter, error) {
	return nil, errors.New("interface traffic sampling is not supported on this platform")
}
