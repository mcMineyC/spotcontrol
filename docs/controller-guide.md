# Controller Guide

This guide covers the `controller` package in depth — the high-level API for controlling Spotify Connect devices. It covers device management, playback control, track loading, event subscriptions, metadata fetching, and volume debouncing.

## Table of Contents

- [Overview](#overview)
- [Creating a Controller](#creating-a-controller)
- [Lifecycle](#lifecycle)
- [Device Management](#device-management)
- [Playback Control](#playback-control)
- [Loading Tracks](#loading-tracks)
- [Playing Playlists](#playing-playlists)
- [Playing Tracks via Connect-State](#playing-tracks-via-connect-state)
- [Queue Management](#queue-management)
- [Transferring Playback](#transferring-playback)
- [Volume Control & Debouncing](#volume-control--debouncing)
- [Player State](#player-state)
- [Event Subscriptions](#event-subscriptions)
- [Track Metadata](#track-metadata)
- [Command Routing](#command-routing)
- [Error Handling](#error-handling)
- [Concurrency](#concurrency)

---

## Overview

The `Controller` is the primary interface for interacting with Spotify Connect devices. It:

- Maintains a **cached view** of the account's device cluster via real-time dealer WebSocket push messages
- Sends playback commands through the **connect-state protocol** (same as the Spotify desktop client) with automatic fallback to the Web API
- Provides **event subscriptions** via Go channels for device list changes, playback state changes, and track metadata updates
- Automatically **fetches rich metadata** from Spotify's private metadata API when the playing track changes
- **Debounces volume** updates (500ms, matching librespot) to prevent rate limiting

All methods on `Controller` are safe for concurrent use.

---

## Creating a Controller

### Via `Session.NewController()` (recommended)

The simplest approach — the session pre-configures everything:

```go
sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    DeviceType:  spotcontrol.DeviceTypeComputer,
    DeviceName:  "MyApp",
    Credentials: session.InteractiveCredentials{CallbackPort: 0},
})
if err != nil {
    log.Fatal(err)
}
defer sess.Close()

ctrl := sess.NewController()
defer ctrl.Close()

if err := ctrl.Start(ctx); err != nil {
    log.Fatal(err)
}
```

### Via `controller.NewController()` (advanced)

For custom configurations or when you want more control:

```go
ctrl := controller.NewController(controller.Config{
    Log:            spotcontrol.NewSimpleLogger(nil),
    Spclient:       sess.Spclient(),
    Dealer:         sess.Dealer(),
    DeviceId:       sess.DeviceId(),
    DeviceName:     "MyApp",
    DeviceType:     spotcontrol.DeviceTypeComputer,
    UseWebApi:      false,           // use spclient proxy (recommended)
    VolumeDebounce: 500 * time.Millisecond, // default; set negative to disable
})
defer ctrl.Close()

if err := ctrl.Start(ctx); err != nil {
    log.Fatal(err)
}
```

### Via `quick.Connect()` (convenience)

The highest-level option — handles everything including state persistence:

```go
result, err := quick.Connect(ctx, quick.QuickConfig{
    StatePath:   "state.json",
    Interactive: true,
})
if err != nil {
    log.Fatal(err)
}
defer result.Close()

// result.Controller is ready to use
// result.Session is the underlying session
```

---

## Lifecycle

The controller lifecycle consists of three phases:

### 1. Create

```go
ctrl := sess.NewController()
// or: ctrl := controller.NewController(cfg)
```

At this point the controller exists but is not processing events.

### 2. Start

```go
err := ctrl.Start(ctx)
```

`Start` performs the following:

1. **Connects the dealer** WebSocket (if not already connected)
2. **Subscribes to dealer messages**:
   - `hm://pusher/v1/connections/` — carries the `Spotify-Connection-Id` header
   - `hm://connect-state/v1/cluster` — cluster state updates
3. **Starts background goroutines** for processing connection ID messages and cluster updates
4. When the first `Spotify-Connection-Id` arrives, **`RegisterDevice`** is called automatically to announce this controller to the connect-state backend

After `Start`, the controller begins receiving real-time cluster updates and is ready to send commands.

### 3. Close

```go
ctrl.Close()
```

- Stops all background goroutines
- Cancels any pending volume debounce timer
- Closes all event subscriber channels
- Safe to call multiple times

---

## Device Management

### Listing Devices from Cluster (instant)

```go
devices := ctrl.ListDevices()
for _, d := range devices {
    fmt.Printf("%-20s %-10s vol=%3d%% active=%v\n",
        d.Name, d.Type, d.Volume, d.IsActive)
}
```

This reads from the cached cluster state — no network request. Returns `nil` if no cluster update has been received yet.

### Listing Devices from Web API

```go
// Prefers cluster cache; falls back to Web API if cache is empty
devices, err := ctrl.ListDevicesFromAPI(ctx)

// Always queries the Web API, bypassing cache
devices, err := ctrl.ListDevicesFromAPIForced(ctx)
```

### Getting the Active Device

```go
activeId := ctrl.ActiveDeviceId() // from cached cluster, or ""
```

### DeviceInfo Fields

| Field | Type | Description |
|-------|------|-------------|
| `Id` | `string` | Unique device identifier |
| `Name` | `string` | Human-readable name |
| `Type` | `string` | Device type (e.g. `"COMPUTER"`, `"SPEAKER"`) |
| `IsActive` | `bool` | Currently active playback device |
| `Volume` | `int` | Volume percentage (0–100), normalized from 0–65535 |
| `SupportsVolume` | `bool` | Whether the device supports volume control |

---

## Playback Control

All playback methods accept a `deviceId` parameter. Pass `""` (empty string) to target the currently active device.

### Play / Pause

```go
// Resume playback
err := ctrl.Play(ctx, "")

// Resume on a specific device
err := ctrl.Play(ctx, "abc123def456")

// Pause
err := ctrl.Pause(ctx, "")
```

### Skip Next / Previous

```go
err := ctrl.Next(ctx, "")
err := ctrl.Previous(ctx, "")
```

### Seek

```go
// Seek to 1 minute 30 seconds
err := ctrl.Seek(ctx, 90000, "")
```

### Shuffle

```go
err := ctrl.SetShuffle(ctx, true, "")   // enable
err := ctrl.SetShuffle(ctx, false, "")  // disable
```

### Repeat

```go
err := ctrl.SetRepeat(ctx, "off", "")      // no repeat
err := ctrl.SetRepeat(ctx, "context", "")  // repeat playlist/album
err := ctrl.SetRepeat(ctx, "track", "")    // repeat current track
```

**Implementation note:** The connect-state protocol uses two separate boolean commands (`set_repeating_context` and `set_repeating_track`) instead of the Web API's single string value. The controller translates between them automatically.

---

## Loading Tracks

`LoadTrack` plays tracks via the Web API style `PUT /v1/me/player/play` endpoint (routed through the spclient proxy by default):

### Play a Single Track

```go
err := ctrl.LoadTrack(ctx, []string{
    "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
}, nil)
```

### Play Multiple Tracks

```go
err := ctrl.LoadTrack(ctx, []string{
    "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
    "spotify:track:5TFCp6cxCaJRhbdn6IWEGh",
    "spotify:track:3n3Ppam7vgaVa1iaRUc9Lp",
}, nil)
```

### Play an Album or Playlist Context

```go
err := ctrl.LoadTrack(ctx, nil, &controller.LoadTrackOptions{
    ContextURI: "spotify:album:4aawyAB9vmqN3uQ7FjRGTy",
})
```

### Start from a Specific Track in a Context

```go
err := ctrl.LoadTrack(ctx, nil, &controller.LoadTrackOptions{
    ContextURI: "spotify:playlist:5ese9XhQqKHoQg4WJ4sZef",
    OffsetURI:  "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
})
```

### Start from a Specific Position

```go
// Start a track at the 30-second mark
err := ctrl.LoadTrack(ctx, []string{
    "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
}, &controller.LoadTrackOptions{
    PositionMs: 30000,
})
```

### Play on a Specific Device

```go
err := ctrl.LoadTrack(ctx, []string{
    "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
}, &controller.LoadTrackOptions{
    DeviceId: "target_device_id",
})
```

---

## Playing Playlists

`PlayPlaylist` uses the connect-state player command endpoint, matching the format the Spotify desktop client sends. This provides better integration with Spotify's recommendations and queue systems compared to `LoadTrack`.

### Basic Playlist Playback

```go
// Play a playlist by its 22-character base62 ID
err := ctrl.PlayPlaylist(ctx, "5ese9XhQqKHoQg4WJ4sZef", nil)
```

### Playlist with Shuffle

```go
err := ctrl.PlayPlaylist(ctx, "5ese9XhQqKHoQg4WJ4sZef", &controller.PlayPlaylistOptions{
    Shuffle: true,
})
```

### Start from a Specific Track

```go
err := ctrl.PlayPlaylist(ctx, "5ese9XhQqKHoQg4WJ4sZef", &controller.PlayPlaylistOptions{
    SkipToTrackURI: "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
})
```

### Play on a Specific Device

```go
err := ctrl.PlayPlaylist(ctx, "5ese9XhQqKHoQg4WJ4sZef", &controller.PlayPlaylistOptions{
    DeviceId: "target_device_id",
})
```

**Fallback behavior:** If the connect-state command fails (e.g. no connection ID), `PlayPlaylist` falls back to the Web API `PUT /v1/me/player/play` with a `context_uri` body.

---

## Playing Tracks via Connect-State

`PlayTrack` sends tracks directly through the connect-state player command endpoint without a playlist or album context. This is useful when you want to play specific tracks without Spotify adding context-based recommendations.

### Play a Single Track

```go
err := ctrl.PlayTrack(ctx, []string{
    "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
}, nil)
```

### Play Multiple Tracks with Skip

```go
idx := 2
err := ctrl.PlayTrack(ctx, []string{
    "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
    "spotify:track:5TFCp6cxCaJRhbdn6IWEGh",
    "spotify:track:3n3Ppam7vgaVa1iaRUc9Lp",
}, &controller.PlayTrackOptions{
    SkipToIndex: &idx, // Start from the third track
})
```

### With Shuffle

```go
err := ctrl.PlayTrack(ctx, []string{
    "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
    "spotify:track:5TFCp6cxCaJRhbdn6IWEGh",
}, &controller.PlayTrackOptions{
    Shuffle: true,
})
```

### `LoadTrack` vs `PlayTrack` vs `PlayPlaylist`

| Feature | `LoadTrack` | `PlayTrack` | `PlayPlaylist` |
|---------|-------------|-------------|----------------|
| **Protocol** | Web API style (`PUT /v1/me/player/play`) | Connect-state command | Connect-state command |
| **Context support** | Yes (album, playlist, artist) | No (tracks only) | Yes (playlist) |
| **Recommendations** | Spotify may add radio tracks | No — plays exactly what you specify | Spotify context behavior |
| **Shuffle control** | Via separate `SetShuffle` call | Built into the command | Built into the command |
| **Fallback** | N/A (always Web API style) | Web API `PUT /v1/me/player/play` | Web API with `context_uri` |
| **Use case** | General-purpose playback | Play specific tracks, no extras | Play a playlist |

---

## Queue Management

### Add a Track to the Queue

```go
err := ctrl.AddToQueue(ctx, "spotify:track:6rqhFgbbKwnb9MLmUQDhG6", "")
```

This sends an `add_to_queue` command via the connect-state protocol. Falls back to `POST /v1/me/player/queue` if unavailable.

---

## Transferring Playback

### Transfer and Start Playing

```go
err := ctrl.TransferPlayback(ctx, "target_device_id", true)
```

### Transfer but Keep Paused

```go
err := ctrl.TransferPlayback(ctx, "target_device_id", false)
```

**Implementation:**

1. **Connect-state transfer** (preferred): Uses `POST /connect-state/v1/connect/transfer/from/{from}/to/{to}`, matching librespot's transfer endpoint
2. **Web API fallback**: Uses `PUT /v1/me/player` with `device_ids` body

The source device is automatically determined from the cached cluster state (active device), or the controller's own device ID if unknown.

---

## Volume Control & Debouncing

### Setting Volume

```go
// Set volume to 75%
err := ctrl.SetVolume(ctx, 75, "")
```

The `volumePercent` parameter is 0–100. Internally, it's converted to the connect-state 0–65535 range.

### How Debouncing Works

To prevent flooding the backend during rapid volume adjustments (e.g. holding a slider), volume updates are **debounced** with a 500ms delay (matching librespot's `VOLUME_UPDATE_DELAY`):

1. When `SetVolume` is called, the volume value is stored and a 500ms timer starts
2. If `SetVolume` is called again before the timer fires, the timer resets and the new value replaces the old one
3. When the timer fires, the most recent volume value is sent to the backend

This means rapid calls like:

```go
ctrl.SetVolume(ctx, 50, "")  // stored, timer starts
ctrl.SetVolume(ctx, 60, "")  // stored, timer resets
ctrl.SetVolume(ctx, 70, "")  // stored, timer resets
// ... 500ms passes ...
// only volume=70 is sent to the backend
```

### Configuring Debounce Duration

```go
ctrl := controller.NewController(controller.Config{
    // ...
    VolumeDebounce: 200 * time.Millisecond,  // faster response
})

// Or disable debouncing entirely:
ctrl := controller.NewController(controller.Config{
    // ...
    VolumeDebounce: -1,  // negative = no debouncing
})
```

### Volume Endpoint

Volume is sent via the dedicated connect-state volume signaling endpoint:

```
PUT /connect-state/v1/connect/volume/from/{fromDevice}/to/{toDevice}
```

This is the same mechanism librespot uses (`SetVolumeCommand` protobuf). If the connect-state path is unavailable, it falls back to `PUT /v1/me/player/volume`.

---

## Player State

### Get Current State (cluster-first)

```go
state, err := ctrl.GetPlayerState(ctx)
if err != nil {
    log.Fatal(err)
}
if state == nil {
    fmt.Println("No active playback")
} else {
    fmt.Printf("Playing: %v\n", state.IsPlaying)
    fmt.Printf("Track:   %s\n", state.TrackURI)
    fmt.Printf("Position: %dms / %dms\n", state.PositionMs, state.DurationMs)
}
```

This tries the cached cluster first (instant, no rate limits), then falls back to the Web API.

### Get State from Cluster Only (instant)

```go
state := ctrl.GetPlayerStateFromCluster()
// Returns nil if no cluster data is available
```

### Get State from Web API Only

```go
state, err := ctrl.GetPlayerStateFromAPI(ctx)
// Always makes a network request (subject to rate limits)
```

### PlayerState Fields

| Field | Type | Description |
|-------|------|-------------|
| `IsPlaying` | `bool` | `true` if playing (not paused) |
| `TrackURI` | `string` | Current track URI |
| `ContextURI` | `string` | Current context (album, playlist, etc.) |
| `PositionMs` | `int64` | Estimated current position (accounts for elapsed time and playback speed) |
| `DurationMs` | `int64` | Total track duration |
| `DeviceId` | `string` | Active device ID |
| `Shuffle` | `bool` | Shuffle enabled |
| `RepeatContext` | `bool` | Context repeat enabled |
| `RepeatTrack` | `bool` | Track repeat enabled |

**Position estimation:** When playback is active, the `PositionMs` returned from the cluster is the position *as of a timestamp*. The controller automatically adds elapsed wall-clock time (scaled by playback speed) to produce an estimated current position, clamped to the track duration.

---

## Event Subscriptions

The controller provides three event channels for real-time notifications. All channels are buffered (capacity 16) and closed when the controller is closed.

### Device List Events

```go
deviceCh := ctrl.SubscribeDeviceList()

go func() {
    for evt := range deviceCh {
        fmt.Printf("Device change (reason=%s):\n", evt.Reason)
        for _, d := range evt.Devices {
            active := ""
            if d.IsActive {
                active = " [ACTIVE]"
            }
            fmt.Printf("  %s (%s) vol=%d%%%s\n", d.Name, d.Type, d.Volume, active)
        }
    }
}()
```

**`DeviceListEvent` fields:**

| Field | Type | Description |
|-------|------|-------------|
| `Devices` | `[]DeviceInfo` | Full snapshot of all devices |
| `DevicesThatChanged` | `[]string` | IDs of devices that triggered this update |
| `Reason` | `string` | Update reason (e.g. `"NEW_DEVICE_APPEARED"`, `"DEVICES_DISAPPEARED"`, `"DEVICE_STATE_CHANGED"`) |

### Playback Events

```go
playbackCh := ctrl.SubscribePlayback()

go func() {
    for evt := range playbackCh {
        s := evt.State
        status := "⏸ Paused"
        if s.IsPlaying {
            status = "▶ Playing"
        }
        fmt.Printf("%s: %s [%dms/%dms]\n", status, s.TrackURI, s.PositionMs, s.DurationMs)
    }
}()
```

**`PlaybackEvent` fields:**

| Field | Type | Description |
|-------|------|-------------|
| `State` | `PlayerState` | Current playback state snapshot |

Playback events are emitted on every cluster update, which includes play/pause, track changes, seek, shuffle/repeat toggles, and more.

### Metadata Events

```go
metadataCh := ctrl.SubscribeMetadata()

go func() {
    for evt := range metadataCh {
        m := evt.Metadata
        fmt.Printf("Now playing: %q by %q on %q\n", m.Title, m.Artist, m.Album)
        if m.ImageURL != "" {
            fmt.Printf("Cover art: %s\n", m.ImageURL)
        }
    }
}()
```

**`MetadataEvent` fields:**

| Field | Type | Description |
|-------|------|-------------|
| `Metadata` | `TrackMetadata` | Rich metadata fetched from the private API |

**How metadata events work:**

1. When a cluster update arrives, the controller checks if the track URI has changed
2. If the track changed, a background goroutine fetches metadata from `GET /metadata/4/track/{hex_id}?market=from_token`
3. Once the fetch completes (typically < 1 second), the `MetadataEvent` is emitted
4. The metadata is also cached for access via `GetTrackMetadata()`

**Important:** Metadata events only fire when the track *changes*. You won't receive a metadata event for play/pause or seek within the same track.

### Multiple Subscribers

Each `Subscribe*` method creates a new independent channel. Multiple subscribers are supported:

```go
ch1 := ctrl.SubscribePlayback()  // subscriber 1
ch2 := ctrl.SubscribePlayback()  // subscriber 2
// Both channels receive every PlaybackEvent
```

### Dropped Events

If a subscriber's channel buffer is full (16 events), new events are **dropped** rather than blocking the update loop. A debug-level log message is emitted when this happens. This design prevents a slow subscriber from affecting other subscribers or the controller's main processing.

### Channel Closure

All subscriber channels are closed when `ctrl.Close()` is called. Use the two-value receive form to detect closure:

```go
evt, ok := <-playbackCh
if !ok {
    // Controller was closed
    return
}
```

Or range over the channel:

```go
for evt := range playbackCh {
    // Process event
}
// Channel closed — controller was shut down
```

---

## Track Metadata

### Cached Metadata (instant, no network)

```go
meta := ctrl.GetTrackMetadata()
if meta != nil {
    fmt.Printf("Title:  %s\n", meta.Title)
    fmt.Printf("Artist: %s\n", meta.Artist)
    fmt.Printf("Album:  %s\n", meta.Album)
    fmt.Printf("Cover:  %s\n", meta.ImageURL)
}
```

The cache is populated automatically whenever the playing track changes (via the background metadata fetch).

### Fetch Metadata for Any Track

```go
meta, err := ctrl.FetchTrackMetadata(ctx, "spotify:track:5TFCp6cxCaJRhbdn6IWEGh")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("%s by %s (%dms)\n", meta.Title, meta.Artist, meta.DurationMs)
```

This always makes a network request and can be used for any track, not just the current one.

### Fetch Metadata for the Current Track

```go
meta, err := ctrl.FetchCurrentTrackMetadata(ctx)
if meta == nil && err == nil {
    fmt.Println("No track playing")
} else if err != nil {
    log.Fatal(err)
} else {
    fmt.Printf("Now playing: %s\n", meta.Title)
}
```

### TrackMetadata Fields

| Field | Type | Description |
|-------|------|-------------|
| `TrackURI` | `string` | Spotify URI (e.g. `"spotify:track:..."`) |
| `Title` | `string` | Track title |
| `Artist` | `string` | Primary artist name |
| `Album` | `string` | Album name |
| `DurationMs` | `int64` | Duration in milliseconds |
| `ImageURL` | `string` | URL of the largest album cover art |
| `SmallImageURL` | `string` | URL of the 64px cover art |
| `ArtistURI` | `string` | Spotify URI for the primary artist (from cluster) |
| `AlbumURI` | `string` | Spotify URI for the album (from cluster) |
| `Raw` | `*spclient.TrackMetadata` | Full raw metadata response |

**Accessing additional metadata via `Raw`:**

```go
meta, _ := ctrl.FetchTrackMetadata(ctx, "spotify:track:...")

// Release date
if meta.Raw.Album != nil && meta.Raw.Album.Date != nil {
    fmt.Printf("Released: %d-%02d-%02d\n",
        meta.Raw.Album.Date.Year,
        meta.Raw.Album.Date.Month,
        meta.Raw.Album.Date.Day)
}

// All artists
for _, a := range meta.Raw.Artist {
    fmt.Printf("Artist: %s\n", a.Name)
}

// Track number
fmt.Printf("Track %d on disc %d\n", meta.Raw.Number, meta.Raw.DiscNumber)

// ISRC
for _, ext := range meta.Raw.ExternalId {
    if ext.Type == "isrc" {
        fmt.Printf("ISRC: %s\n", ext.Id)
    }
}

// All cover art sizes
if meta.Raw.Album != nil && meta.Raw.Album.CoverGroup != nil {
    for _, img := range meta.Raw.Album.CoverGroup.Image {
        fmt.Printf("%s: https://i.scdn.co/image/%s (%dx%d)\n",
            img.Size, img.FileId, img.Width, img.Height)
    }
}
```

### Metadata API Details

Track metadata is fetched from the private spclient endpoint:

```
GET /metadata/4/track/{hex_id}?market=from_token
```

This endpoint:
- Uses the **Login5 bearer token** (not the OAuth2 Web API token)
- Returns **JSON** with detailed track information
- Responses are typically **CDN-cached** with a long max-age
- Does **not** require OAuth2 scopes — works with just Login5 authentication

---

## Command Routing

### Default: Spclient Proxy

By default, the controller routes commands through two paths:

1. **Connect-state endpoints** (preferred) for playback control:
   - `POST /connect-state/v1/player/command/from/{from}/to/{to}` — player commands
   - `PUT /connect-state/v1/connect/volume/from/{from}/to/{to}` — volume
   - `POST /connect-state/v1/connect/transfer/from/{from}/to/{to}` — transfer

2. **Spclient proxy** (fallback) for Web API-style endpoints:
   - `/v1/me/player/play`, `/v1/me/player/pause`, etc.
   - These go through the spclient infrastructure, avoiding the public `api.spotify.com` rate limits

### Web API Mode

If `UseWebApi: true` is set in the config, commands bypass the connect-state endpoints and go directly to `api.spotify.com`. This is **not recommended** because:

- Subject to stricter rate limits
- Requires a valid OAuth2 token (not just Login5)
- Some features may be slower or less reliable

```go
ctrl := controller.NewController(controller.Config{
    // ...
    UseWebApi: true,  // not recommended
})
```

### Automatic Fallback

All connect-state methods automatically fall back to the Web API proxy if:
- No connection ID is available (dealer not connected)
- No target device can be determined
- The connect-state endpoint returns an error

A warning is logged when fallback occurs.

---

## Error Handling

### Common Errors

| Error | Cause | Solution |
|-------|-------|----------|
| `"command failed with status 403"` | Device doesn't support the command | Check device capabilities |
| `"command failed with status 404"` | No active playback or device not found | Ensure a device is active |
| `"no connection ID available"` | Dealer not connected or connection ID not yet received | Wait for dealer connection; check `ctrl.Start()` succeeded |
| `"failed connecting dealer"` | Network issue or invalid token | Check network; verify authentication |

### Pattern: Retry with Transfer

A common pattern when no device is active:

```go
err := ctrl.Play(ctx, "")
if err != nil {
    // Try to find a device and transfer to it
    devices, _ := ctrl.ListDevicesFromAPI(ctx)
    if len(devices) > 0 {
        if err := ctrl.TransferPlayback(ctx, devices[0].Id, true); err != nil {
            log.Fatal(err)
        }
    }
}
```

---

## Concurrency

The `Controller` is fully safe for concurrent use:

- **Cluster state** is protected by `sync.RWMutex` — reads are concurrent, writes are serialized
- **Connection ID** is protected by its own `sync.RWMutex`
- **Event subscribers** are protected by `sync.RWMutex` — new subscriptions can be added concurrently
- **Volume debounce** state is protected by `sync.Mutex`
- **Metadata cache** is protected by `sync.RWMutex`
- **Background goroutines** communicate through channels and don't share mutable state

You can safely call methods from multiple goroutines:

```go
// Safe: concurrent playback control from multiple goroutines
go func() { ctrl.SetVolume(ctx, 50, "") }()
go func() { ctrl.Next(ctx, "") }()
go func() { state, _ := ctrl.GetPlayerState(ctx) }()
```

---

## Next Steps

- **[Package Reference](package-reference.md)** — Full API reference for all types and methods
- **[Protocol Details](protocol-details.md)** — Wire-level details of the connect-state protocol
- **[Examples](examples.md)** — Walkthrough of the example applications
- **[Getting Started](getting-started.md)** — Quick start guide