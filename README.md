# lib-yttv

Go library for talking to YouTube TV: channel guide, per-airing → live-channel resolution, and Chromecast playback control via the Lounge protocol.

## Status

Pre-alpha. API surface is in flux.

## Packages

| Package     | Purpose                                                                                                     |
| ----------- | ----------------------------------------------------------------------------------------------------------- |
| `auth`      | Credentials struct, cookie/env loader, SAPISIDHASH, Lounge XSRF token derivation                            |
| `innertube` | HTTP client for `tv.youtube.com/youtubei/v1/{browse,next,player}` with the WEB_UNPLUGGED context            |
| `epg`       | Parse `FEunplugged_epg` into a channel list + resolve per-airing → live videoIds via `/next`                |
| `cast`      | mDNS discovery and Cast v2 protocol: connect, LAUNCH `32EAB1DF` (YouTube TV receiver), extract screenId     |
| `lounge`    | `get_lounge_token_batch`, authenticated bind, `setPlaylist`+`setSubtitlesTrack` command, optional long-poll |

The top-level `Session` ties these together into a single high-level API for callers.

## Required user inputs

- `DATASYNC_ID` (21-digit Google account ID from `ytcfg.data_.DATASYNC_ID`)
- Cookie set captured from a **single-account** `tv.youtube.com` session

## Example

```go
creds := &auth.Creds{
    GoogleAccountID: os.Getenv("DATASYNC_ID"),
    SAPISID:         os.Getenv("SAPISID"),
    Secure3PSID:     os.Getenv("SECURE_3PSID"),
}
sess := yttv.New(creds)

channels, _ := sess.Channels(ctx)
for _, ch := range channels {
    fmt.Printf("%s — now: %s\n", ch.Name, ch.CurrentTitle)
}

devices, _ := discover.Discover(ctx, 5*time.Second)
_ = sess.Cast(ctx, devices[0], "ESPN")
```
