// Package cast handles mDNS discovery of Chromecast devices and the v2 Cast
// protocol calls needed to LAUNCH the YouTube TV receiver app and extract its
// screenId — the handle that authorizes the Lounge session.
package cast

import (
	"net"
	"strconv"
)

type Device struct {
	Name         string
	Host         string
	Port         int
	UUID         string // mDNS `id` TXT; stable identity across IP changes
	Capabilities uint64 // mDNS `ca` TXT bitmask
}

// IsVideo reports whether the device has a video output (ca bit 0).
func (d Device) IsVideo() bool { return d.Capabilities&0x01 != 0 }

// ID returns the stable identity for the device: UUID when known, else host:port.
func (d Device) ID() string {
	if d.UUID != "" {
		return d.UUID
	}
	return net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
}
