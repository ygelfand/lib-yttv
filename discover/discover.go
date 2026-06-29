// Package discover performs mDNS discovery for Chromecast devices on the
// local network, as one-shot scans (Discover) or a continuous watch (Watch).
package discover

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/ygelfand/lib-yttv/cast"
)

const service = "_googlecast._tcp"

// Discover performs a single mDNS browse and returns devices seen within the
// timeout window.
func Discover(ctx context.Context, timeout time.Duration) ([]cast.Device, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("mdns resolver: %w", err)
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	browseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := resolver.Browse(browseCtx, service, "local.", entries); err != nil {
		return nil, fmt.Errorf("mdns browse: %w", err)
	}
	out := []cast.Device{}
	for e := range entries {
		if d, ok := parseEntry(e); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// Event is a device appearance (Up=true) or disappearance (Up=false) emitted
// by Watch.
type Event struct {
	Device cast.Device
	Up     bool
}

// Watch continuously discovers devices and emits Up/Down events on the returned
// channel until ctx is cancelled. It re-browses every `interval` (each scan
// lasting `window`) and tracks last-seen times: a device unseen for two
// intervals is considered gone. The channel is closed when ctx ends.
//
// grandcat/zeroconf emits each device once per browse and never signals
// removal, so the periodic re-browse + last-seen sweep is what synthesizes
// reliable up/down (the same approach pychromecast's HostBrowser uses).
func Watch(ctx context.Context, interval, window time.Duration) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		type seen struct {
			dev  cast.Device
			last time.Time
		}
		known := map[string]seen{}
		expiry := 2 * interval

		scan := func() {
			devs, err := Discover(ctx, window)
			if err != nil {
				return
			}
			now := time.Now()
			for _, d := range devs {
				id := d.ID()
				prev, ok := known[id]
				known[id] = seen{dev: d, last: now}
				if !ok {
					emit(ctx, out, Event{Device: d, Up: true})
				} else if changed(prev.dev, d) {
					// re-announce up with refreshed fields (e.g. IP change)
					emit(ctx, out, Event{Device: d, Up: true})
				}
			}
			for id, s := range known {
				if now.Sub(s.last) > expiry {
					delete(known, id)
					emit(ctx, out, Event{Device: s.dev, Up: false})
				}
			}
		}

		scan()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				scan()
			}
		}
	}()
	return out
}

func emit(ctx context.Context, out chan<- Event, e Event) {
	select {
	case out <- e:
	case <-ctx.Done():
	}
}

func changed(a, b cast.Device) bool {
	return a.Host != b.Host || a.Port != b.Port || a.Name != b.Name
}

func parseEntry(e *zeroconf.ServiceEntry) (cast.Device, bool) {
	if len(e.AddrIPv4) == 0 {
		return cast.Device{}, false
	}
	d := cast.Device{
		Name: friendlyName(e),
		Host: e.AddrIPv4[0].String(),
		Port: e.Port,
	}
	if d.Name == "" {
		return cast.Device{}, false
	}
	for _, t := range e.Text {
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		switch k {
		case "id":
			d.UUID = v
		case "ca":
			if c, err := strconv.ParseUint(v, 10, 64); err == nil {
				d.Capabilities = c
			}
		}
	}
	return d, true
}

func friendlyName(e *zeroconf.ServiceEntry) string {
	for _, t := range e.Text {
		if name, ok := strings.CutPrefix(t, "fn="); ok {
			return name
		}
	}
	return e.Instance
}
