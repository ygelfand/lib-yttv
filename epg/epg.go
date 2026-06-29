// Package epg parses the FEunplugged_epg Innertube response into a flat
// channel list, each carrying the live channel videoId the Chromecast receiver
// can actually play.
package epg

import (
	"context"
	"errors"
	"time"

	"github.com/tidwall/gjson"
	"github.com/ygelfand/lib-yttv/innertube"
)

const epgBrowseID = "FEunplugged_epg"

// Channel is one row of the EPG. The Current* and LiveVideoID fields describe
// the currently-airing program; Airings is the full schedule.
type Channel struct {
	Name           string
	StationIconURL string
	LiveVideoID    string
	ClickTracking  string
	CurrentTitle   string
	Airings        []Airing
}

// LiveThumbnailURL is a current frame of the live broadcast (vs the static
// program art in Airing.ThumbnailURL). Empty if LiveVideoID is unknown.
func (c Channel) LiveThumbnailURL() string {
	if c.LiveVideoID == "" {
		return ""
	}
	return "https://i.ytimg.com/vi/" + c.LiveVideoID + "/maxresdefault_live.jpg"
}

// Airing is one scheduled program. VideoID is the per-airing videoId (not
// playable); LiveVideoID is the live channel videoId to cast.
type Airing struct {
	BeginTimeMs   int64
	EndTimeMs     int64
	VideoID       string
	LiveVideoID   string // navigationEndpoint.watchEndpoint.videoId; playable
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
				LiveVideoID:   ar.Get("navigationEndpoint.watchEndpoint.videoId").String(),
				Title:         ar.Get("title.runs.0.text").String(),
				Subtitle:      ar.Get("subtitle.runs.0.text").String(),
				Synopsis:      ar.Get("quaternaryText.runs.0.text").String(),
				ThumbnailURL:  ar.Get("thumbnail.thumbnails.0.url").String(),
				ClickTracking: ar.Get("navigationEndpoint.clickTrackingParams").String(),
				IsLive:        now >= begin && now < end,
			}
			ch.Airings = append(ch.Airings, airing)
			if airing.IsLive {
				ch.LiveVideoID = airing.LiveVideoID
				ch.ClickTracking = airing.ClickTracking
				ch.CurrentTitle = airing.Title
			}
			return true
		})
		if ch.LiveVideoID == "" {
			return true
		}
		out = append(out, ch)
		return true
	})
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
