// Package discover performs mDNS discovery for Chromecast devices on the
package discover

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/ygelfand/lib-yttv/cast"
)

// Discover performs mDNS browse on _googlecast._tcp.local. and returns devices
// observed within the timeout window. Friendly name comes from the `fn=` TXT
// record, falling back to the mDNS instance label.
func Discover(ctx context.Context, timeout time.Duration) ([]cast.Device, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("mdns resolver: %w", err)
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	browseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := resolver.Browse(browseCtx, "_googlecast._tcp", "local.", entries); err != nil {
		return nil, fmt.Errorf("mdns browse: %w", err)
	}
	out := []cast.Device{}
	for e := range entries {
		if len(e.AddrIPv4) == 0 {
			continue
		}
		name := friendlyName(e)
		if name == "" {
			continue
		}
		out = append(out, cast.Device{
			Name: name,
			Host: e.AddrIPv4[0].String(),
			Port: e.Port,
		})
	}
	return out, nil
}

func friendlyName(e *zeroconf.ServiceEntry) string {
	for _, t := range e.Text {
		if name, ok := strings.CutPrefix(t, "fn="); ok {
			return name
		}
	}
	return e.Instance
}
