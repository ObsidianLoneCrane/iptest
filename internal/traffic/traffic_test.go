package traffic

import (
	"context"
	"errors"
	"math"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestCounterParsersPreserveIndependentInterfaces(t *testing.T) {
	for _, test := range []struct {
		name string
		read func() (map[string]counter, error)
		want map[string]counter
	}{
		{"linux", func() (map[string]counter, error) {
			return parseLinux("Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n eth0: 1000 1 0 0 0 0 0 0 2000 1 0 0 0 0 0 0\n tun0: 3000 1 0 0 0 0 0 0 4000 1 0 0 0 0 0 0\n")
		}, map[string]counter{"eth0": {1000, 2000}, "tun0": {3000, 4000}}},
		{"windows array", func() (map[string]counter, error) {
			return parseWindows([]byte(`[{"Name":"Ethernet","ReceivedBytes":1000,"SentBytes":2000},{"Name":"以太网","ReceivedBytes":3000,"SentBytes":4000}]`))
		}, map[string]counter{"Ethernet": {1000, 2000}, "以太网": {3000, 4000}}},
		{"windows singleton", func() (map[string]counter, error) {
			return parseWindows([]byte(`{"Name":"Ethernet","ReceivedBytes":0,"SentBytes":18446744073709551615}`))
		}, map[string]counter{"Ethernet": {0, math.MaxUint64}}},
		{"mac link rows", func() (map[string]counter, error) {
			return parseDarwin("Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\nen0 1500 <Link#6> aa:bb:cc:dd:ee:ff 10 0 1000 20 0 2000 0\nen0 1500 192.168.1 192.168.1.2 10 0 9999 20 0 9999 0\nutun0 1380 <Link#7> 12 0 3000 22 0 4000 0\n")
		}, map[string]counter{"en0": {1000, 2000}, "utun0": {3000, 4000}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.read()
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("counters=%v, error=%v; want %v", got, err, test.want)
			}
		})
	}
}

func TestCounterParsersRejectMissingOrInvalidByteCounts(t *testing.T) {
	for _, test := range []struct {
		name string
		read func() (map[string]counter, error)
	}{
		{"linux negative", func() (map[string]counter, error) { return parseLinux("eth0: -1 1 0 0 0 0 0 0 2 1 0 0 0 0 0 0") }},
		{"linux short", func() (map[string]counter, error) { return parseLinux("eth0: 1 2") }},
		{"linux empty", func() (map[string]counter, error) { return parseLinux("") }},
		{"windows null", func() (map[string]counter, error) { return parseWindows([]byte(`null`)) }},
		{"windows missing", func() (map[string]counter, error) {
			return parseWindows([]byte(`{"Name":"Ethernet","ReceivedBytes":1}`))
		}},
		{"windows negative", func() (map[string]counter, error) {
			return parseWindows([]byte(`{"Name":"Ethernet","ReceivedBytes":-1,"SentBytes":2}`))
		}},
		{"windows fractional", func() (map[string]counter, error) {
			return parseWindows([]byte(`{"Name":"Ethernet","ReceivedBytes":1.5,"SentBytes":2}`))
		}},
		{"mac missing byte columns", func() (map[string]counter, error) {
			return parseDarwin("Name Mtu Network Address Ipkts\nen0 1500 <Link#1> aa:bb 10")
		}},
		{"mac invalid count", func() (map[string]counter, error) {
			return parseDarwin("Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\nen0 1500 <Link#1> aa:bb 10 0 bad 20 0 30 0")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.read(); err == nil {
				t.Fatal("invalid counters accepted")
			}
		})
	}
}

func TestSampleFiltersLoopbackResetAndNewInterfaces(t *testing.T) {
	calls := 0
	read := func(context.Context) (map[string]counter, error) {
		calls++
		if calls == 1 {
			return map[string]counter{"eth0": {1000, 2000}, "tun0": {3000, 4000}, "reset": {9000, 9000}, "lo": {100, 100}, "down": {100, 100}}, nil
		}
		return map[string]counter{"eth0": {1100, 2200}, "tun0": {3300, 4400}, "reset": {1, 1}, "lo": {9000, 9000}, "down": {9000, 9000}, "new": {100, 100}}, nil
	}
	interfaces := func() ([]net.Interface, error) {
		return []net.Interface{{Name: "eth0", Flags: net.FlagUp}, {Name: "tun0", Flags: net.FlagUp}, {Name: "reset", Flags: net.FlagUp}, {Name: "lo", Flags: net.FlagUp | net.FlagLoopback}, {Name: "down"}, {Name: "new", Flags: net.FlagUp}}, nil
	}
	rates, err := sample(context.Background(), 5*time.Millisecond, read, interfaces)
	if err != nil || len(rates) != 2 || rates[0].Interface != "eth0" || rates[1].Interface != "tun0" {
		t.Fatalf("rates=%v,error=%v", rates, err)
	}
	if rates[0].ReceiveBytesPerSecond <= 0 || math.Abs(rates[0].SendBytesPerSecond/rates[0].ReceiveBytesPerSecond-2) > 0.001 || math.Abs(rates[1].ReceiveBytesPerSecond/rates[0].ReceiveBytesPerSecond-3) > 0.001 {
		t.Fatalf("bad rates: %v", rates)
	}
}

func TestSampleUsesActualElapsedTimeIncludingReadDelay(t *testing.T) {
	calls := 0
	read := func(context.Context) (map[string]counter, error) {
		calls++
		if calls == 2 {
			time.Sleep(35 * time.Millisecond)
		}
		return map[string]counter{"eth0": {uint64(calls) * 1000, 0}}, nil
	}
	interfaces := func() ([]net.Interface, error) { return []net.Interface{{Name: "eth0", Flags: net.FlagUp}}, nil }
	rates, err := sample(context.Background(), 5*time.Millisecond, read, interfaces)
	if err != nil || len(rates) != 1 || rates[0].ReceiveBytesPerSecond >= 50000 {
		t.Fatalf("read delay was excluded from elapsed time: rates=%v,error=%v", rates, err)
	}
}

func TestInterfaceGoingDownDuringSamplingIsNotReported(t *testing.T) {
	reads := 0
	read := func(context.Context) (map[string]counter, error) {
		reads++
		return map[string]counter{"tun0": {uint64(reads) * 1000, 0}}, nil
	}
	checks := 0
	interfaces := func() ([]net.Interface, error) {
		checks++
		flags := net.FlagUp
		if checks > 1 {
			flags = 0
		}
		return []net.Interface{{Name: "tun0", Flags: flags}}, nil
	}
	rates, err := sample(context.Background(), time.Millisecond, read, interfaces)
	if err == nil || len(rates) != 0 {
		t.Fatalf("unstable interface reported: %v, %v", rates, err)
	}
}

func TestSampleCancellationAndReadFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	read := func(context.Context) (map[string]counter, error) {
		cancel()
		return map[string]counter{"eth0": {1, 1}}, nil
	}
	interfaces := func() ([]net.Interface, error) { return []net.Interface{{Name: "eth0", Flags: net.FlagUp}}, nil }
	if _, err := sample(ctx, time.Second, read, interfaces); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := sample(context.Background(), 0, read, interfaces); err == nil {
		t.Fatal("zero sampling duration accepted")
	}
	if _, err := sample(nil, time.Second, read, interfaces); err == nil {
		t.Fatal("nil context accepted")
	}
	want := errors.New("fixture read failure")
	if _, err := sample(context.Background(), time.Millisecond, func(context.Context) (map[string]counter, error) { return nil, want }, interfaces); !errors.Is(err, want) {
		t.Fatalf("read error lost: %v", err)
	}
}
