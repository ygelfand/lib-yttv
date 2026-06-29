package cast

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ygelfand/lib-yttv/cast/castpb"
	"google.golang.org/protobuf/proto"
)

const (
	nsMedia     = "urn:x-cast:com.google.cast.media"
	nsMultizone = "urn:x-cast:com.google.multizone"
)

// Status is a read-only snapshot of what a Cast device is doing. App is nil
// when the device is idle (no running app or only the backdrop); Media is nil
// when the running app reports no active media.
type Status struct {
	AppID       string  // receiver application id (e.g. 32EAB1DF for YouTube TV)
	AppName     string  // displayName, e.g. "YouTube TV", "Netflix"
	Idle        bool    // true when idle/backdrop
	Volume      float64 // 0..1
	Muted       bool
	TransportID string
	Media       *Media
}

// Media describes the active media on a device, parsed generically from
// MEDIA_STATUS so it works for any app, not just YouTube TV.
type Media struct {
	PlayerState    string  // PLAYING, PAUSED, BUFFERING, IDLE
	ContentID      string  // app content id; for YouTube this is the videoId
	Title          string  // metadata.title
	Subtitle       string  // artist/seriesTitle/subtitle/album, whichever is set
	ImageURL       string  // first metadata image (https-normalized)
	StreamType     string  // LIVE or BUFFERED
	CurrentTime    float64 // seconds
	Duration       float64 // seconds (0 for LIVE)
	MediaSessionID int
}

// GetStatus queries the receiver for the running app + volume, then (if an app
// is running) connects to its transport and queries media status. Read-only.
func (r *Receiver) GetStatus(ctx context.Context) (*Status, error) {
	if err := r.request(nsReceiver, defaultReceiver, message{Type: "GET_STATUS"}); err != nil {
		return nil, err
	}
	m, err := r.waitForNS(ctx, nsReceiver)
	if err != nil {
		return nil, err
	}
	st := parseReceiver(m.GetPayloadUtf8())
	if st.Idle || st.TransportID == "" {
		return st, nil
	}

	if err := r.request(nsConnection, st.TransportID, message{Type: "CONNECT"}); err != nil {
		return st, nil // app status is still useful even if we can't reach media
	}
	if err := r.request(nsMedia, st.TransportID, message{Type: "GET_STATUS"}); err != nil {
		return st, nil
	}
	// Media may never arrive (app without media); cap the wait and treat a
	// timeout as "no media" rather than an error.
	mctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if mm, err := r.waitForNS(mctx, nsMedia); err == nil {
		st.Media = parseMedia(mm.GetPayloadUtf8())
	}
	return st, nil
}

// SetVolume sets the device volume level (0..1). Receiver-level; no media needed.
func (r *Receiver) SetVolume(level float64) error {
	if level < 0 {
		level = 0
	} else if level > 1 {
		level = 1
	}
	return r.sendRaw(nsReceiver, defaultReceiver, map[string]any{
		"type": "SET_VOLUME", "volume": map[string]any{"level": level},
	})
}

// SetMuted mutes or unmutes the device.
func (r *Receiver) SetMuted(muted bool) error {
	return r.sendRaw(nsReceiver, defaultReceiver, map[string]any{
		"type": "SET_VOLUME", "volume": map[string]any{"muted": muted},
	})
}

// PlayPause toggles play/pause for the active media. It fetches status to find
// the transport + media session, then sends the opposite of the current state.
func (r *Receiver) PlayPause(ctx context.Context) error {
	st, err := r.GetStatus(ctx)
	if err != nil {
		return err
	}
	if st.Media == nil || st.TransportID == "" {
		return nil
	}
	msgType := "PAUSE"
	if st.Media.PlayerState == "PAUSED" {
		msgType = "PLAY"
	}
	return r.sendRaw(nsMedia, st.TransportID, map[string]any{
		"type": msgType, "mediaSessionId": st.Media.MediaSessionID,
	})
}

// sendRaw marshals an arbitrary payload (auto-assigning requestId) and sends it.
func (r *Receiver) sendRaw(ns, dest string, payload map[string]any) error {
	if _, ok := payload["requestId"]; !ok {
		payload["requestId"] = r.reqID.Add(1)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeFrame(r.conn, castMessage(ns, dest, string(body)))
}

func parseReceiver(payload string) *Status {
	var msg struct {
		Type   string `json:"type"`
		Status struct {
			Volume struct {
				Level float64 `json:"level"`
				Muted bool    `json:"muted"`
			} `json:"volume"`
			Applications []struct {
				AppID        string `json:"appId"`
				DisplayName  string `json:"displayName"`
				IsIdleScreen bool   `json:"isIdleScreen"`
				TransportID  string `json:"transportId"`
			} `json:"applications"`
		} `json:"status"`
	}
	st := &Status{Idle: true}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		return st
	}
	st.Volume = msg.Status.Volume.Level
	st.Muted = msg.Status.Volume.Muted
	for _, app := range msg.Status.Applications {
		if app.IsIdleScreen {
			continue
		}
		st.Idle = false
		st.AppID = app.AppID
		st.AppName = app.DisplayName
		st.TransportID = app.TransportID
		break
	}
	return st
}

func parseMedia(payload string) *Media {
	var msg struct {
		Status []struct {
			PlayerState    string  `json:"playerState"`
			CurrentTime    float64 `json:"currentTime"`
			MediaSessionID int     `json:"mediaSessionId"`
			Media          struct {
				ContentID  string  `json:"contentId"`
				StreamType string  `json:"streamType"`
				Duration   float64 `json:"duration"`
				Metadata   struct {
					Title       string `json:"title"`
					Subtitle    string `json:"subtitle"`
					Artist      string `json:"artist"`
					SeriesTitle string `json:"seriesTitle"`
					AlbumName   string `json:"albumName"`
					Images      []struct {
						URL string `json:"url"`
					} `json:"images"`
				} `json:"metadata"`
			} `json:"media"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil || len(msg.Status) == 0 {
		return nil
	}
	s := msg.Status[0]
	md := s.Media.Metadata
	m := &Media{
		PlayerState:    s.PlayerState,
		ContentID:      s.Media.ContentID,
		Title:          md.Title,
		Subtitle:       firstNonEmpty(md.Subtitle, md.Artist, md.SeriesTitle, md.AlbumName),
		StreamType:     s.Media.StreamType,
		CurrentTime:    s.CurrentTime,
		Duration:       s.Media.Duration,
		MediaSessionID: s.MediaSessionID,
	}
	if len(md.Images) > 0 {
		m.ImageURL = normalizeURL(md.Images[0].URL)
	}
	return m
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeURL(s string) string {
	if len(s) >= 2 && s[:2] == "//" {
		return "https:" + s
	}
	return s
}

// castMessage builds a STRING CastMessage frame for an arbitrary payload.
func castMessage(ns, dest, payload string) *castpb.CastMessage {
	return &castpb.CastMessage{
		ProtocolVersion: castpb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        proto.String(defaultSender),
		DestinationId:   proto.String(dest),
		Namespace:       proto.String(ns),
		PayloadType:     castpb.CastMessage_STRING.Enum(),
		PayloadUtf8:     proto.String(payload),
	}
}

// Convenience: open a connection, fetch status, and close.
func GetDeviceStatus(ctx context.Context, d Device) (*Status, error) {
	r, err := Connect(ctx, d)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.GetStatus(ctx)
}
