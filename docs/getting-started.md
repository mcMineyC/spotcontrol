# Getting Started

This guide walks you through installing spotcontrol, authenticating with Spotify, and controlling playback on your devices.

## Prerequisites

- **Go 1.23** or later
- A **Spotify account** (Free or Premium — though some playback control features require Premium)

## Installation

Add spotcontrol to your Go module:

```sh
go get github.com/mcMineyC/spotcontrol
```

## Quick Start: One-Liner with `quick.Connect()`

The simplest way to get started is the `quick.Connect()` function, which handles session creation, authentication, state persistence, and controller setup in a single call:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/mcMineyC/spotcontrol/quick"
)

func main() {
    ctx := context.Background()

    // Connect handles everything: load/save state, authenticate,
    // create session + controller, start the dealer.
    result, err := quick.Connect(ctx, quick.QuickConfig{
        StatePath:   "spotcontrol_state.json", // persists device ID, credentials, OAuth2 tokens
        DeviceName:  "MyApp",
        Interactive: true, // OAuth2 PKCE login if no stored credentials
    })
    if err != nil {
        log.Fatal(err)
    }
    defer result.Close()

    fmt.Printf("Connected as: %s\n", result.Session.Username())

    // List devices.
    devices := result.ListDevices()
    for _, d := range devices {
        fmt.Printf("Device: %s (%s) active=%v\n", d.Name, d.Type, d.IsActive)
    }

    // Resume playback on the active device.
    if err := result.Play(ctx, ""); err != nil {
        log.Fatal(err)
    }
}
```

### What Happens on First Run

1. `quick.Connect()` checks for a saved state file at the `StatePath` — if it doesn't exist, no error is returned; it just means "no prior state."
2. Since no stored credentials exist and `Interactive: true`, it starts an OAuth2 PKCE flow:
   - A local HTTP server is started on a random port to receive the callback.
   - An authorization URL is printed to the console.
   - You open the URL in your browser and log in to Spotify.
   - The callback server receives the authorization code and exchanges it for tokens.
3. The session connects to the Spotify access point (AP), authenticates with Login5, and initializes the spclient and dealer WebSocket.
4. Credentials (device ID, username, stored credentials, and OAuth2 tokens) are automatically saved to `spotcontrol_state.json`.
5. On subsequent runs, the stored credentials are used and no browser interaction is needed.

### `QuickConfig` Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `StatePath` | `string` | `""` | Path to the JSON state file for persistence. If empty, no state is saved/loaded. |
| `DeviceName` | `string` | `"SpotControl"` | Human-readable name shown in Spotify Connect device lists. |
| `DeviceType` | `spotcontrol.DeviceType` | `DeviceTypeComputer` | Device type (Computer, Speaker, Smartphone, etc.). |
| `DeviceId` | `string` | auto-generated | 40-hex-char device identifier. Auto-generated if empty. |
| `Interactive` | `bool` | `false` | Enable OAuth2 PKCE browser-based login when no stored credentials exist. |
| `CallbackPort` | `int` | `0` | Port for the OAuth2 callback server. `0` = random available port. |
| `Log` | `spotcontrol.Logger` | `NewSimpleLogger(nil)` | Logger instance. |

### `ConnectResult` Methods

The `ConnectResult` returned by `quick.Connect()` provides convenient pass-through methods for all common operations:

| Method | Description |
|--------|-------------|
| `Play(ctx, deviceId)` | Resume playback |
| `Pause(ctx, deviceId)` | Pause playback |
| `Next(ctx, deviceId)` | Skip to next track |
| `Previous(ctx, deviceId)` | Skip to previous track |
| `SetVolume(ctx, percent, deviceId)` | Set volume (0–100) |
| `Seek(ctx, positionMs, deviceId)` | Seek to position |
| `SetShuffle(ctx, state, deviceId)` | Enable/disable shuffle |
| `SetRepeat(ctx, state, deviceId)` | Set repeat mode (`"off"`, `"context"`, `"track"`) |
| `LoadTrack(ctx, uris, opts)` | Load and play tracks by URI |
| `PlayTrack(ctx, uris, opts)` | Play tracks via connect-state (no context) |
| `AddToQueue(ctx, uri, deviceId)` | Add a track to the queue |
| `TransferPlayback(ctx, deviceId, play)` | Transfer playback to another device |
| `ListDevices()` | List devices from cached cluster state |
| `ListDevicesFromAPI(ctx)` | List devices from Web API |
| `GetPlayerState(ctx)` | Get current playback state |
| `SubscribeDeviceList()` | Subscribe to device list change events |
| `SubscribePlayback()` | Subscribe to playback state change events |
| `SubscribeMetadata()` | Subscribe to track metadata change events |
| `GetTrackMetadata()` | Get cached metadata for the current track |
| `FetchTrackMetadata(ctx, uri)` | Fetch metadata for any track URI |
| `FetchCurrentTrackMetadata(ctx)` | Fetch metadata for the current track |
| `Close()` | Shut down controller and session |

For all methods that accept a `deviceId` string, passing `""` (empty string) targets the currently active device.

---

## Advanced: Manual Session + Controller

For full control over session configuration, use `session.NewSessionFromOptions` and `controller.NewController` directly:

```go
package main

import (
    "context"
    "fmt"
    "log"

    spotcontrol "github.com/mcMineyC/spotcontrol"
    "github.com/mcMineyC/spotcontrol/controller"
    "github.com/mcMineyC/spotcontrol/session"
)

func main() {
    ctx := context.Background()

    sess, err := session.NewSessionFromOptions(ctx, &session.Options{
        Log:        spotcontrol.NewSimpleLogger(nil),
        DeviceType: spotcontrol.DeviceTypeComputer,
        DeviceName: "MyApp",
        // DeviceId is auto-generated if empty.
        Credentials: session.InteractiveCredentials{
            CallbackPort: 0, // random port
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer sess.Close()

    // Save credentials for next time.
    state := sess.ExportState()
    if err := spotcontrol.SaveState("state.json", state); err != nil {
        log.Printf("warning: failed saving state: %v", err)
    }

    // Create and start the controller.
    ctrl := sess.NewController()
    defer ctrl.Close()

    if err := ctrl.Start(ctx); err != nil {
        log.Fatal(err)
    }

    // List devices via the Web API.
    devices, err := ctrl.ListDevicesFromAPI(ctx)
    if err != nil {
        log.Fatal(err)
    }
    for _, d := range devices {
        fmt.Printf("Device: %s (%s) active=%v vol=%d%%\n", d.Name, d.Type, d.IsActive, d.Volume)
    }

    // Play a track.
    err = ctrl.LoadTrack(ctx, []string{"spotify:track:6rqhFgbbKwnb9MLmUQDhG6"}, nil)
    if err != nil {
        log.Fatal(err)
    }
}
```

### Session Lifecycle

The manual approach gives you explicit control over each step:

1. **Create session** — `session.NewSessionFromOptions(ctx, opts)` performs the full connection flow (client token → AP resolve → AP connect → Login5 → spclient → dealer → Mercury).
2. **Export state** — `sess.ExportState()` captures device ID, username, stored credentials, and OAuth2 tokens into an `AppState` struct.
3. **Save state** — `spotcontrol.SaveState(path, state)` writes the `AppState` as JSON with `0600` permissions.
4. **Create controller** — `sess.NewController()` creates a pre-configured controller from the session.
5. **Start controller** — `ctrl.Start(ctx)` connects the dealer WebSocket, subscribes to cluster updates, and begins processing.
6. **Use the API** — Call methods on `ctrl` to control playback.
7. **Clean up** — `ctrl.Close()` stops background goroutines; `sess.Close()` closes all connections.

---

## Using Stored Credentials

Once you've authenticated and saved state, subsequent sessions can skip the interactive login:

```go
// Load previously saved state.
state, err := spotcontrol.LoadState("state.json")
if err != nil {
    log.Fatal(err)
}
if state == nil {
    log.Fatal("no saved state found — run with interactive login first")
}

sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    Log:        spotcontrol.NewSimpleLogger(nil),
    DeviceType: spotcontrol.DeviceTypeComputer,
    DeviceId:   state.DeviceId,
    DeviceName: "MyApp",
    Credentials: session.StoredCredentials{
        Username: state.Username,
        Data:     state.StoredCredentials,
    },
    AppState: state, // restores OAuth2 token for Web API access
})
if err != nil {
    log.Fatal(err)
}
defer sess.Close()
```

### The `AppState` Struct

`AppState` is the persistence model for spotcontrol. It captures everything needed to restore a session:

| Field | Type | Description |
|-------|------|-------------|
| `DeviceId` | `string` | 40-hex-char unique device identifier |
| `Username` | `string` | Spotify username |
| `StoredCredentials` | `[]byte` | Reusable auth credentials from APWelcome |
| `OAuthAccessToken` | `string` | OAuth2 access token for the Web API |
| `OAuthRefreshToken` | `string` | OAuth2 refresh token |
| `OAuthTokenType` | `string` | Token type (typically `"Bearer"`) |
| `OAuthExpiry` | `time.Time` | Token expiration time |

The `SaveState` function writes this as JSON with `0600` permissions (owner read/write only) to protect credentials. `LoadState` reads it back, returning `(nil, nil)` if the file doesn't exist (not an error).

---

## Choosing a Device Type

spotcontrol re-exports device type constants from the protobuf package so you don't need to import deeply nested protobuf packages:

| Constant | Description |
|----------|-------------|
| `spotcontrol.DeviceTypeComputer` | Desktop/laptop computer |
| `spotcontrol.DeviceTypeTablet` | Tablet device |
| `spotcontrol.DeviceTypeSmartphone` | Smartphone |
| `spotcontrol.DeviceTypeSpeaker` | Smart speaker |
| `spotcontrol.DeviceTypeTV` | Television |
| `spotcontrol.DeviceTypeAVR` | Audio/video receiver |
| `spotcontrol.DeviceTypeSTB` | Set-top box |
| `spotcontrol.DeviceTypeAudioDongle` | Audio dongle (e.g. Chromecast Audio) |
| `spotcontrol.DeviceTypeGameConsole` | Game console |
| `spotcontrol.DeviceTypeCastVideo` | Chromecast video |
| `spotcontrol.DeviceTypeCastAudio` | Chromecast audio |
| `spotcontrol.DeviceTypeAutomobile` | Car |
| `spotcontrol.DeviceTypeSmartwatch` | Smartwatch |
| `spotcontrol.DeviceTypeChromebook` | Chromebook |
| `spotcontrol.DeviceTypeCarThing` | Spotify Car Thing |

The device type affects how your controller appears in Spotify Connect device lists on other clients.

---

## Generating a Device ID

If you need to generate a device ID manually (rather than letting the session auto-generate one):

```go
deviceId := spotcontrol.GenerateDeviceId()
// Returns a 40-character lowercase hex string (20 random bytes from crypto/rand)
// Example: "a1b2c3d4e5f6a1b2c3d4a1b2c3d4e5f6a1b2c3d4"
```

The device ID must be exactly 40 hex characters (20 bytes). It is validated when passed to `session.Options.DeviceId`.

---

## Logging

spotcontrol defines a `Logger` interface compatible with logrus-style structured loggers. Three implementations are provided:

### SimpleLogger (default)

Writes human-readable log lines to an `io.Writer`. Trace-level messages are suppressed.

```go
log := spotcontrol.NewSimpleLogger(nil) // writes to os.Stderr
log := spotcontrol.NewSimpleLogger(os.Stdout)
```

### SlogLogger

Adapts Go's standard `log/slog.Logger` to the spotcontrol `Logger` interface.

```go
import "log/slog"

slogger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
log := spotcontrol.NewSlogLogger(slogger)
```

### NullLogger

Discards all log output. Useful for testing or when you want complete silence.

```go
log := &spotcontrol.NullLogger{}
```

### Custom Logger

Implement the `Logger` interface to integrate with your own logging framework:

```go
type Logger interface {
    Tracef(format string, args ...interface{})
    Debugf(format string, args ...interface{})
    Infof(format string, args ...interface{})
    Warnf(format string, args ...interface{})
    Errorf(format string, args ...interface{})

    Trace(args ...interface{})
    Debug(args ...interface{})
    Info(args ...interface{})
    Warn(args ...interface{})
    Error(args ...interface{})

    WithField(key string, value interface{}) Logger
    WithError(err error) Logger
}
```

---

## Next Steps

- **[Architecture](architecture.md)** — Understand how the components fit together
- **[Controller Guide](controller-guide.md)** — Deep dive into playback control, events, and metadata
- **[Authentication](authentication.md)** — All supported authentication methods
- **[Examples](examples.md)** — Walk through the included example applications