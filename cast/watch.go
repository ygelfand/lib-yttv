package cast

import (
	"context"
	"errors"
	"net"
	"time"
)

const (
	streamReadTick = time.Second      // read deadline granularity (also ctx responsiveness)
	streamPing     = 5 * time.Second  // heartbeat cadence
	streamPoll     = 30 * time.Second // periodic GET_STATUS refresh
	streamStall    = 20 * time.Second // no frames for this long => reconnect
)

// WatchDevice maintains a persistent connection to a device and emits a fresh
// *Status whenever its app, media, or volume changes. It reconnects with
// backoff on drops and closes the channel when ctx is cancelled. Latest-wins:
// if the consumer lags, intermediate updates are dropped.
func WatchDevice(ctx context.Context, d Device) <-chan *Status {
	out := make(chan *Status, 1)
	go func() {
		defer close(out)
		backoff := time.Second
		for ctx.Err() == nil {
			r, err := Connect(ctx, d)
			if err != nil {
				if !sleep(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, 30*time.Second)
				continue
			}
			backoff = time.Second
			_ = r.stream(ctx, func(s *Status) { emitStatus(out, s) })
			_ = r.Close()
			if !sleep(ctx, time.Second) { // brief pause before reconnect
				return
			}
		}
	}()
	return out
}

// stream runs the single-goroutine read loop: it parses pushed status frames,
// PONGs heartbeats, and sends its own keepalive/refresh inline on read
// timeouts. Returns on ctx cancel or any connection error (for reconnect).
func (r *Receiver) stream(ctx context.Context, onStatus func(*Status)) error {
	if err := r.request(nsReceiver, defaultReceiver, message{Type: "GET_STATUS"}); err != nil {
		return err
	}
	cur := &Status{Idle: true}
	connectedTransport := ""
	lastFrame := time.Now()
	nextPing := lastFrame.Add(streamPing)
	nextPoll := lastFrame.Add(streamPoll)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = r.conn.SetReadDeadline(time.Now().Add(streamReadTick))
		m, err := readFrame(r.conn)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				now := time.Now()
				if now.Sub(lastFrame) > streamStall {
					return errors.New("cast: connection stalled")
				}
				if now.After(nextPing) {
					_ = r.request(nsHeartbeat, defaultReceiver, message{Type: "PING"})
					nextPing = now.Add(streamPing)
				}
				if now.After(nextPoll) {
					_ = r.request(nsReceiver, defaultReceiver, message{Type: "GET_STATUS"})
					nextPoll = now.Add(streamPoll)
				}
				continue
			}
			return err
		}
		lastFrame = time.Now()

		switch m.GetNamespace() {
		case nsHeartbeat:
			if payloadType(m.GetPayloadUtf8()) == "PING" {
				_ = r.request(nsHeartbeat, m.GetSourceId(), message{Type: "PONG"})
			}
		case nsReceiver:
			st := parseReceiver(m.GetPayloadUtf8())
			if st.Idle {
				connectedTransport = ""
				cur = st
			} else {
				st.Media = cur.Media // keep last media until a media update arrives
				cur = st
				if st.TransportID != "" && st.TransportID != connectedTransport {
					connectedTransport = st.TransportID
					_ = r.request(nsConnection, st.TransportID, message{Type: "CONNECT"})
					_ = r.request(nsMedia, st.TransportID, message{Type: "GET_STATUS"})
				}
			}
			onStatus(cloneStatus(cur))
		case nsMedia:
			if md := parseMedia(m.GetPayloadUtf8()); md != nil {
				cur.Media = md
				onStatus(cloneStatus(cur))
			}
		}
	}
}

// emitStatus delivers s latest-wins: it never blocks the read loop (which must
// keep PONGing). A pending stale status is dropped in favor of the newest.
func emitStatus(out chan *Status, s *Status) {
	select {
	case out <- s:
		return
	default:
	}
	select {
	case <-out:
	default:
	}
	select {
	case out <- s:
	default:
	}
}

func cloneStatus(s *Status) *Status {
	c := *s
	if s.Media != nil {
		m := *s.Media
		c.Media = &m
	}
	return &c
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
