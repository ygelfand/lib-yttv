// Package innertube is a thin HTTP client for tv.youtube.com/youtubei/v1/*.
// It sets the WEB_UNPLUGGED (clientName=41) context and the SAPISIDHASH
// authorization headers required for authenticated calls.
package innertube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ygelfand/lib-yttv/auth"
	"github.com/ygelfand/lib-yttv/constants"
)

type Client struct {
	HTTP  *http.Client
	Creds *auth.Creds
}

func New(creds *auth.Creds) *Client {
	return &Client{
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Creds: creds,
	}
}

// context returns the Innertube `context.client` object embedded in every request.
// unpluggedAppInfo.filterModeType=1 is what live YTTV sends and is required for
// the EPG to be populated with current airings.
func (c *Client) context() map[string]any {
	return map[string]any{
		"client": map[string]any{
			"clientName":    constants.ClientName,
			"clientVersion": constants.ClientVersion,
			"hl":            "en",
			"gl":            "US",
			"unpluggedAppInfo": map[string]any{
				"filterModeType": "1",
			},
		},
	}
}

// Browse calls /youtubei/v1/browse?alt=json with the given browseId and
// returns the raw response body. Useful browseIds: FEunplugged_home,
// FEunplugged_browse, FEunplugged_epg.
func (c *Client) Browse(ctx context.Context, browseID string) ([]byte, error) {
	return c.post(ctx, "/youtubei/v1/browse?alt=json", map[string]any{
		"context":  c.context(),
		"browseId": browseID,
	})
}

// Next calls /youtubei/v1/next?alt=json. For a per-airing videoId from EPG,
// the response embeds the live channel videoId.
func (c *Client) Next(ctx context.Context, videoID string) ([]byte, error) {
	return c.post(ctx, "/youtubei/v1/next?alt=json", map[string]any{
		"context": c.context(),
		"videoId": videoID,
	})
}

func (c *Client) post(ctx context.Context, path string, body map[string]any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, constants.Origin+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	ts := time.Now().Unix()
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", c.Creds.AuthorizationHeader(ts))
	req.Header.Set("x-origin", constants.Origin)
	req.Header.Set("x-youtube-client-name", constants.ClientName)
	req.Header.Set("x-youtube-client-version", constants.ClientVersion)
	c.Creds.AddCookies(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("innertube %s: status=%d body=%s", path, resp.StatusCode, raw)
	}
	return raw, nil
}
