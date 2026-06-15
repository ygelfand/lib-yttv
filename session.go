// Package yttv is the high-level facade over the auth/innertube/epg/cast/lounge
// packages. Callers that just want "list channels, cast to TV" use Session;
// callers that need lower-level control reach into the subpackages directly.
package yttv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ygelfand/lib-yttv/auth"
	"github.com/ygelfand/lib-yttv/cast"
	"github.com/ygelfand/lib-yttv/epg"
	"github.com/ygelfand/lib-yttv/innertube"
	"github.com/ygelfand/lib-yttv/lounge"
)

type Session struct {
	Creds     *auth.Creds
	Innertube *innertube.Client
}

func New(creds *auth.Creds) *Session {
	return &Session{
		Creds:     creds,
		Innertube: innertube.New(creds),
	}
}

func (s *Session) Channels(ctx context.Context) ([]epg.Channel, error) {
	return epg.Fetch(ctx, s.Innertube)
}

// Cast targets a known Cast device with a channel name. Callers handle
// discovery (use the discover package) and name → device resolution.
func (s *Session) Cast(ctx context.Context, dev cast.Device, channelName string) error {
	slog.InfoContext(ctx, "fetching channel guide")
	channels, err := s.Channels(ctx)
	if err != nil {
		return fmt.Errorf("fetch channels: %w", err)
	}
	var ch *epg.Channel
	want := strings.ToLower(channelName)
	for i := range channels {
		if strings.Contains(strings.ToLower(channels[i].Name), want) {
			ch = &channels[i]
			break
		}
	}
	if ch == nil {
		return fmt.Errorf("channel %q not found", channelName)
	}
	slog.InfoContext(ctx, "matched channel", "name", ch.Name, "per_airing", ch.PerAiringVideoID)

	liveID, err := epg.ResolveLiveVideoID(ctx, s.Innertube, ch.PerAiringVideoID)
	if err != nil {
		return fmt.Errorf("resolve live videoId: %w", err)
	}
	slog.InfoContext(ctx, "resolved live videoId", "video_id", liveID)

	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	recv, err := cast.Connect(connCtx, dev)
	if err != nil {
		return fmt.Errorf("cast connect: %w", err)
	}
	defer recv.Close()
	slog.InfoContext(ctx, "connected; launching receiver")

	screenID, err := recv.Launch(connCtx)
	if err != nil {
		return fmt.Errorf("cast launch: %w", err)
	}
	slog.InfoContext(ctx, "got screenId", "screen_id", screenID)

	loungeToken, err := lounge.GetLoungeToken(ctx, s.Innertube.HTTP, screenID)
	if err != nil {
		return fmt.Errorf("lounge token: %w", err)
	}
	slog.InfoContext(ctx, "got lounge token")

	slog.InfoContext(ctx, "binding lounge session")
	sess, err := lounge.Bind(ctx, s.Innertube.HTTP, s.Creds, screenID, loungeToken)
	if err != nil {
		return fmt.Errorf("lounge bind: %w", err)
	}
	slog.InfoContext(ctx, "bound", "sid", sess.SID, "gsessionid", sess.GSessionID)

	if err := sess.SetPlaylist(ctx, liveID, ch.ClickTracking); err != nil {
		return fmt.Errorf("setPlaylist: %w", err)
	}
	slog.InfoContext(ctx, "setPlaylist sent")
	return nil
}

var ErrNotImplemented = errors.New("not implemented")
