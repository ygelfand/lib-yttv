// Package cast handles mDNS discovery of Chromecast devices and the v2 Cast
// protocol calls needed to LAUNCH the YouTube TV receiver app and extract its
// screenId — the handle that authorizes the Lounge session.
package cast

type Device struct {
	Name string
	Host string
	Port int
}
