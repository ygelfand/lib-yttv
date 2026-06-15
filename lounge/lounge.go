// Package lounge implements the authenticated YouTube TV Lounge protocol:
// token exchange, bind (long-poll session), and channel-switch commands.
package lounge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/ygelfand/lib-yttv/auth"
	"github.com/ygelfand/lib-yttv/constants"
)

const (
	bindURL  = constants.Origin + "/api/lounge/bc/bind"
	tokenURL = constants.Origin + "/api/lounge/pairing/get_lounge_token_batch"
)

// GetLoungeToken exchanges a Chromecast screenId for a Lounge token. Anonymous;
// no cookies required. Token TTL is ~14 days.
func GetLoungeToken(ctx context.Context, hc *http.Client, screenID string) (string, error) {
	body := strings.NewReader("screen_ids=" + url.QueryEscape(screenID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("lounge token: status=%d body=%s", resp.StatusCode, truncate(raw, 512))
	}
	var out struct {
		Screens []struct {
			LoungeToken string `json:"loungeToken"`
		} `json:"screens"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("lounge token: decode %s: %w", truncate(raw, 256), err)
	}
	if len(out.Screens) == 0 || out.Screens[0].LoungeToken == "" {
		return "", fmt.Errorf("lounge token: empty token for screenId %s; body=%s", screenID, truncate(raw, 512))
	}
	return out.Screens[0].LoungeToken, nil
}

// Session is one authenticated Lounge connection to a specific screen.
type Session struct {
	HTTP        *http.Client
	Creds       *auth.Creds
	ScreenID    string
	LoungeToken string

	// Server-issued after Bind.
	SID        string
	GSessionID string

	rid atomic.Int64
}

// Bind opens an authenticated Lounge session and parses SID + gsessionid out
// of the initial event stream.
func Bind(ctx context.Context, hc *http.Client, creds *auth.Creds, screenID, loungeToken string) (*Session, error) {
	s := &Session{HTTP: hc, Creds: creds, ScreenID: screenID, LoungeToken: loungeToken}
	s.rid.Store(1000)

	q := url.Values{}
	q.Set("device", "REMOTE_CONTROL")
	q.Set("app", "youtube-desktop")
	q.Set("name", constants.SenderName)
	q.Set("id", constants.SenderID)
	q.Set("theme", "up")

	raw, err := s.post(ctx, q, "count=0")
	if err != nil {
		return nil, fmt.Errorf("lounge bind: %w", err)
	}
	slog.DebugContext(ctx, "lounge bind response", "bytes", len(raw), "body", string(raw))

	if err := s.consumeStream(bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("lounge bind: parse stream: %w; body=%s", err, truncate(raw, 512))
	}
	if s.SID == "" || s.GSessionID == "" {
		return nil, fmt.Errorf("lounge bind: missing SID/gsessionid; body=%s", truncate(raw, 512))
	}
	return s, nil
}

// SetPlaylist switches the receiver to the given live channel videoId.
// clickTrackingParams should come from the EPG row's navigationEndpoint.
func (s *Session) SetPlaylist(ctx context.Context, liveVideoID, clickTrackingParams string) error {
	body := url.Values{}
	body.Set("count", "2")
	body.Set("req0__sc", "setPlaylist")
	body.Set("req0_videoId", liveVideoID)
	if clickTrackingParams != "" {
		body.Set("req0_clickTrackingParams", clickTrackingParams)
	}
	body.Set("req1__sc", "setSubtitlesTrack")
	body.Set("req1_videoId", liveVideoID)

	q := url.Values{}
	q.Set("SID", s.SID)
	q.Set("gsessionid", s.GSessionID)
	raw, err := s.post(ctx, q, body.Encode())
	if err != nil {
		return fmt.Errorf("lounge setPlaylist: %w", err)
	}
	slog.DebugContext(ctx, "lounge setPlaylist response", "bytes", len(raw), "body", string(raw))
	return nil
}

// post sends an authenticated Lounge POST. The caller supplies the per-call
// query params; we add VER, RID (auto-incremented), loungeIdToken, plus the
// shared cookie/xsrf headers.
func (s *Session) post(ctx context.Context, q url.Values, body string) ([]byte, error) {
	q.Set("VER", "8")
	q.Set("RID", strconv.FormatInt(s.rid.Add(1), 10))
	q.Set("loungeIdToken", s.LoungeToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bindURL+"?"+q.Encode(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("x-youtube-lounge-xsrf-token", s.Creds.LoungeXSRFToken())
	s.Creds.AddCookies(req)

	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return raw, fmt.Errorf("status=%d body=%s", resp.StatusCode, truncate(raw, 256))
	}
	return raw, nil
}

// consumeStream reads the length-prefixed JSON event frames from a bind
// response and extracts SID and gsessionid.
//
// Frame format:
//
//	<LENGTH>\n
//	[[N, ["sc", "<value>", ...]], [N+1, ...], ...]
func (s *Session) consumeStream(r io.Reader) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		n, perr := strconv.Atoi(strings.TrimSpace(line))
		if perr != nil || n <= 0 {
			continue
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return err
		}
		var batch [][]any
		if err := json.Unmarshal(buf, &batch); err != nil {
			continue
		}
		for _, ev := range batch {
			if len(ev) < 2 {
				continue
			}
			inner, _ := ev[1].([]any)
			if len(inner) < 2 {
				continue
			}
			sc, _ := inner[0].(string)
			switch sc {
			case "c":
				s.SID, _ = inner[1].(string)
			case "S":
				s.GSessionID, _ = inner[1].(string)
			}
		}
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
