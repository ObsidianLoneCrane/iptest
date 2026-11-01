// Package traffic samples per-interface byte counters without sending traffic.
package traffic

import (
	"context"
	"errors"
	"net"
	"sort"
	"time"
)

// Rate is total interface traffic, including other processes. Interfaces are
// never summed, so physical and TUN counters cannot double-count one total.
type Rate struct {
	Interface             string
	ReceiveBytesPerSecond float64
	SendBytesPerSecond    float64
}

type counter struct{ receive, send uint64 }

// Sample measures active, non-loopback interfaces over the actual elapsed
// interval. New, removed, or reset interfaces are excluded rather than assigned
// a negative rate. The context bounds both counter reads and the sampling wait.
func Sample(ctx context.Context, interval time.Duration) ([]Rate, error) {
	return sample(ctx, interval, readCounters, net.Interfaces)
}

func sample(ctx context.Context, interval time.Duration, read func(context.Context) (map[string]counter, error), interfaces func() ([]net.Interface, error)) ([]Rate, error) {
	if ctx == nil || interval <= 0 {
		return nil, errors.New("traffic sampling requires a context and positive interval")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	available, err := interfaces()
	if err != nil {
		return nil, errors.New("could not enumerate traffic interfaces")
	}
	active := make(map[string]bool)
	for _, iface := range available {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			active[iface.Name] = true
		}
	}
	before, err := read(ctx)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	after, err := read(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seconds := time.Since(started).Seconds()
	current, err := interfaces()
	if err != nil {
		return nil, errors.New("could not verify active traffic interfaces")
	}
	stillActive := make(map[string]bool)
	for _, iface := range current {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			stillActive[iface.Name] = true
		}
	}
	var rates []Rate
	for name, last := range after {
		first, ok := before[name]
		if !active[name] || !stillActive[name] || !ok || last.receive < first.receive || last.send < first.send {
			continue
		}
		rates = append(rates, Rate{Interface: name, ReceiveBytesPerSecond: float64(last.receive-first.receive) / seconds, SendBytesPerSecond: float64(last.send-first.send) / seconds})
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i].Interface < rates[j].Interface })
	if len(rates) == 0 {
		return nil, errors.New("no stable active interface counters were available")
	}
	return rates, nil
}
