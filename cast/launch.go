package cast

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ygelfand/lib-yttv/cast/castpb"
	"github.com/ygelfand/lib-yttv/constants"
)

// Launch starts the YouTube TV Cast receiver app, completes the per-app
// CONNECT handshake, then waits for mdxSessionStatus. Returns data.screenId.
func (r *Receiver) Launch(ctx context.Context) (string, error) {
	if err := r.request(nsReceiver, defaultReceiver, message{Type: "LAUNCH", AppID: constants.YouTubeTVAppID}); err != nil {
		return "", fmt.Errorf("cast launch: %w", err)
	}

	transportID, err := r.waitForString(ctx, nsReceiver, extractTransportID)
	if err != nil {
		return "", fmt.Errorf("cast launch: %w", err)
	}

	if err := r.request(nsConnection, transportID, message{Type: "CONNECT"}); err != nil {
		return "", fmt.Errorf("cast launch: connect to app %s: %w", transportID, err)
	}

	screenID, err := r.waitForString(ctx, nsYouTubeMDX, extractScreenID)
	if err != nil {
		return "", fmt.Errorf("cast launch: %w", err)
	}
	return screenID, nil
}

// Stop issues GET_STATUS, then sends STOP for every running app so the
// receiver returns to idle.
func (r *Receiver) Stop(ctx context.Context) error {
	if err := r.request(nsReceiver, defaultReceiver, message{Type: "GET_STATUS"}); err != nil {
		return fmt.Errorf("cast get_status: %w", err)
	}
	m, err := r.waitForNS(ctx, nsReceiver)
	if err != nil {
		return fmt.Errorf("cast get_status: %w", err)
	}
	for _, app := range parseApps(m.GetPayloadUtf8()) {
		if app.SessionID == "" {
			continue
		}
		if err := r.request(nsReceiver, defaultReceiver, message{Type: "STOP", SessionID: app.SessionID}); err != nil {
			return fmt.Errorf("cast stop %s: %w", app.SessionID, err)
		}
	}
	return nil
}

// waitForString reads frames on `ns` until `extract` returns (value, true).
func (r *Receiver) waitForString(ctx context.Context, ns string, extract func(*castpb.CastMessage) (string, bool)) (string, error) {
	for {
		m, err := r.waitForNS(ctx, ns)
		if err != nil {
			return "", err
		}
		if v, ok := extract(m); ok {
			return v, nil
		}
	}
}

func extractTransportID(m *castpb.CastMessage) (string, bool) {
	for _, app := range parseApps(m.GetPayloadUtf8()) {
		if strings.EqualFold(app.AppID, constants.YouTubeTVAppID) {
			return app.TransportID, true
		}
	}
	return "", false
}

func extractScreenID(m *castpb.CastMessage) (string, bool) {
	var msg struct {
		Type string `json:"type"`
		Data struct {
			ScreenID string `json:"screenId"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(m.GetPayloadUtf8()), &msg); err != nil || msg.Type != "mdxSessionStatus" {
		return "", false
	}
	return msg.Data.ScreenID, msg.Data.ScreenID != ""
}

type application struct {
	AppID       string `json:"appId"`
	SessionID   string `json:"sessionId"`
	TransportID string `json:"transportId"`
}

func parseApps(payload string) []application {
	var msg struct {
		Type   string `json:"type"`
		Status struct {
			Applications []application `json:"applications"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil || msg.Type != "RECEIVER_STATUS" {
		return nil
	}
	return msg.Status.Applications
}
