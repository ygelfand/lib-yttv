// Package epg parses the FEunplugged_epg Innertube response into a flat
// channel list and resolves per-airing videoIds (what EPG returns) to live
// channel videoIds (what the Chromecast receiver can actually play).
package epg

import (
	"context"
	"errors"
	"time"

	"github.com/tidwall/gjson"
	"github.com/ygelfand/lib-yttv/innertube"
)

const epgBrowseID = "FEunplugged_epg"

// Channel is one row of the EPG. The Current* and PerAiringVideoID fields
// describe the currently-airing program; Airings is the full schedule.
type Channel struct {
	Name             string
	StationIconURL   string
	PerAiringVideoID string // NOT directly playable; resolve via ResolveLiveVideoID
	ClickTracking    string
	CurrentTitle     string
	Airings          []Airing
}

// Airing is one scheduled program. VideoID is the per-airing videoId — resolve
// to a live channel videoId via ResolveLiveVideoID before casting.
type Airing struct {
	BeginTimeMs   int64
	EndTimeMs     int64
	VideoID       string
	Title         string
	Subtitle      string
	Synopsis      string
	ThumbnailURL  string
	ClickTracking string
	IsLive        bool
}

// Fetch retrieves the EPG and returns each channel with its full schedule.
// Rows without a currently-airing program are skipped.
func Fetch(ctx context.Context, c *innertube.Client) ([]Channel, error) {
	raw, err := c.Browse(ctx, epgBrowseID)
	if err != nil {
		return nil, err
	}
	rows := gjson.GetBytes(raw, "contents.epgRenderer.paginationRenderer.epgPaginationRenderer.contents")
	if !rows.Exists() {
		return nil, errors.New("epg: response missing expected structure")
	}

	now := time.Now().UnixMilli()
	out := []Channel{}
	rows.ForEach(func(_, row gjson.Result) bool {
		station := row.Get("epgRowRenderer.station.epgStationRenderer")
		name := firstNonEmpty(
			station.Get("name.runs.0.text").String(),
			station.Get("name.simpleText").String(),
			station.Get("icon.accessibility.accessibilityData.label").String(),
			station.Get("callSign.runs.0.text").String(),
		)
		if name == "" {
			return true
		}
		ch := Channel{
			Name:           name,
			StationIconURL: station.Get("icon.thumbnails.0.url").String(),
		}
		row.Get("epgRowRenderer.airings").ForEach(func(_, a gjson.Result) bool {
			ar := a.Get("epgAiringRenderer")
			begin, end := ar.Get("beginTimeMs").Int(), ar.Get("endTimeMs").Int()
			vid := ar.Get("videoId").String()
			if begin == 0 || end == 0 || vid == "" {
				return true
			}
			airing := Airing{
				BeginTimeMs:   begin,
				EndTimeMs:     end,
				VideoID:       vid,
				Title:         ar.Get("title.runs.0.text").String(),
				Subtitle:      ar.Get("subtitle.runs.0.text").String(),
				Synopsis:      ar.Get("quaternaryText.runs.0.text").String(),
				ThumbnailURL:  ar.Get("thumbnail.thumbnails.0.url").String(),
				ClickTracking: ar.Get("navigationEndpoint.clickTrackingParams").String(),
				IsLive:        now >= begin && now < end,
			}
			ch.Airings = append(ch.Airings, airing)
			if airing.IsLive {
				ch.PerAiringVideoID = airing.VideoID
				ch.ClickTracking = airing.ClickTracking
				ch.CurrentTitle = airing.Title
			}
			return true
		})
		if ch.PerAiringVideoID == "" {
			return true
		}
		out = append(out, ch)
		return true
	})
	return out, nil
}

// ResolveLiveVideoID asks /youtubei/v1/next for the live channel videoId that
// the given per-airing videoId redirects to. Returns the original ID if no
// distinct redirect target is present.
func ResolveLiveVideoID(ctx context.Context, c *innertube.Client, perAiringVideoID string) (string, error) {
	raw, err := c.Next(ctx, perAiringVideoID)
	if err != nil {
		return "", err
	}
	live := ""
	gjson.GetBytes(raw, "onResponseReceivedEndpoints.#.watchEndpoint.videoId").ForEach(func(_, v gjson.Result) bool {
		if s := v.String(); s != "" && s != perAiringVideoID {
			live = s
			return false
		}
		return true
	})
	if live == "" {
		return perAiringVideoID, nil
	}
	return live, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
