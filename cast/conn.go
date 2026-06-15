package cast

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ygelfand/lib-yttv/cast/castpb"
	"google.golang.org/protobuf/proto"
)

const (
	defaultSender   = "sender-0"
	defaultReceiver = "receiver-0"

	nsConnection = "urn:x-cast:com.google.cast.tp.connection"
	nsHeartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	nsReceiver   = "urn:x-cast:com.google.cast.receiver"
	nsYouTubeMDX = "urn:x-cast:com.google.youtube.mdx"
)

// Receiver is an open Cast TLS connection. Use Launch to start the YouTube TV
// receiver app and obtain a screenId.
type Receiver struct {
	conn  net.Conn
	reqID atomic.Int64
}

// Connect opens a TLS connection to a Cast device on its port (8009) and
// completes the v2 CONNECT handshake.
func Connect(ctx context.Context, d Device) (*Receiver, error) {
	addr := net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return nil, fmt.Errorf("cast dial %s: %w", addr, err)
	}
	r := &Receiver{conn: conn}
	if err := r.request(nsConnection, defaultReceiver, message{Type: "CONNECT"}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return r, nil
}

// message is the JSON payload shape for Cast control messages
type message struct {
	Type      string `json:"type"`
	RequestID int64  `json:"requestId,omitempty"`
	AppID     string `json:"appId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// request marshals msg (auto-assigning RequestID if zero and Type warrants it)
// and sends it as a CastMessage on the given namespace.
func (r *Receiver) request(namespace, dest string, msg message) error {
	if msg.RequestID == 0 && msg.Type != "CONNECT" && msg.Type != "PONG" {
		msg.RequestID = r.reqID.Add(1)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return writeFrame(r.conn, &castpb.CastMessage{
		ProtocolVersion: castpb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        proto.String(defaultSender),
		DestinationId:   proto.String(dest),
		Namespace:       proto.String(namespace),
		PayloadType:     castpb.CastMessage_STRING.Enum(),
		PayloadUtf8:     proto.String(string(body)),
	})
}

// waitForNS reads frames until one arrives on the given namespace, PONGing
// heartbeat PINGs inline. Honors ctx deadline if set.
func (r *Receiver) waitForNS(ctx context.Context, ns string) (*castpb.CastMessage, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = r.conn.SetReadDeadline(dl)
	}
	for {
		m, err := readFrame(r.conn)
		if err != nil {
			return nil, err
		}
		if m.GetNamespace() == nsHeartbeat {
			if payloadType(m.GetPayloadUtf8()) == "PING" {
				_ = r.request(nsHeartbeat, m.GetSourceId(), message{Type: "PONG"})
			}
			continue
		}
		if m.GetNamespace() == ns {
			return m, nil
		}
	}
}

func (r *Receiver) Close() error { return r.conn.Close() }

func payloadType(s string) string {
	var hdr struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(s), &hdr)
	return hdr.Type
}
