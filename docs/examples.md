# Examples

This document provides detailed walkthroughs of the example applications included with spotcontrol. Both examples demonstrate real-world usage patterns and can serve as starting points for your own applications.

## Table of Contents

- [micro-controller](#micro-controller)
  - [Overview](#micro-controller-overview)
  - [Building](#building-micro-controller)
  - [First Run (Interactive Login)](#first-run-interactive-login)
  - [Subsequent Runs](#subsequent-runs)
  - [Command-Line Flags](#micro-controller-flags)
  - [Available Commands](#available-commands)
  - [Code Walkthrough](#micro-controller-code-walkthrough)
  - [Example Session](#example-session)
- [event-watcher](#event-watcher)
  - [Overview](#event-watcher-overview)
  - [Building](#building-event-watcher)
  - [Running](#running-event-watcher)
  - [Command-Line Flags](#event-watcher-flags)
  - [Code Walkthrough](#event-watcher-code-walkthrough)
  - [Example Output](#example-output)
- [Writing Your Own Application](#writing-your-own-application)

---

## micro-controller

**Location:** `examples/micro-controller/`

### Micro-Controller Overview

The micro-controller is an interactive command-line application for controlling Spotify Connect devices. It demonstrates:

- Authentication via OAuth2 PKCE (interactive) or stored credentials
- State persistence across sessions
- All playback control commands (play, pause, skip, volume, shuffle, repeat, seek)
- Loading tracks by URI and playing playlists
- Playing tracks via the connect-state protocol (no context/recommendations)
- Adding tracks to the playback queue
- Transferring playback between devices
- Listing devices from both the cluster cache and the Web API
- Querying and displaying the current player state
- Fetching track metadata from the private metadata API
- Live event watching (device, playback, and metadata events)
- Parsing Spotify URLs (open.spotify.com links) into URIs
- Interactive device selection

### Building Micro-Controller

```sh
cd examples/micro-controller
go build -o micro-controller
```

Or run directly:

```sh
go run ./examples/micro-controller
```

### First Run (Interactive Login)

On first run, use the `--interactive` flag to authenticate via OAuth2 PKCE:

```sh
./micro-controller --interactive
```

This will:

1. Start a local HTTP server on a random port to receive the OAuth2 callback
2. Print an authorization URL to the console
3. Wait for you to open the URL in your browser and complete login
4. Exchange the authorization code for tokens
5. Connect to Spotify's backend (AP → Login5 → spclient → dealer)
6. Save credentials to `spotcontrol_state.json` (device ID, username, stored credentials, OAuth2 tokens)
7. Start the interactive command prompt

### Subsequent Runs

Once credentials are saved, run without the `--interactive` flag:

```sh
./micro-controller
```

The application loads stored credentials from `spotcontrol_state.json` and authenticates automatically — no browser interaction needed.

### Micro-Controller Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--interactive` | `false` | Use interactive OAuth2 PKCE login flow |
| `--state` | `spotcontrol_state.json` | Path to the state persistence file |
| `--devicename` | `SpotControl` | Name shown in Spotify Connect device lists |
| `--port` | `0` | OAuth2 callback port (`0` = random available port) |

### Available Commands

#### Playback Control

| Command | Description |
|---------|-------------|
| `play` | Resume playback on the active (or selected) device |
| `pause` | Pause playback |
| `next` | Skip to the next track |
| `prev` | Skip to the previous track |
| `volume <0-100>` | Set volume percentage |
| `seek <ms>` | Seek to a position in milliseconds |
| `shuffle <on\|off>` | Enable or disable shuffle mode |
| `repeat <off\|context\|track>` | Set repeat mode |

#### Track Loading

| Command | Description |
|---------|-------------|
| `open <url>` | Play a Spotify URL (track, playlist, or album from `open.spotify.com`) |
| `load <uri1> [uri2 ...]` | Load track(s) by Spotify URI via Web API style endpoint |
| `playtrack <uri1> [uri2 ...]` | Play track(s) via connect-state (no context/recommendations) |
| `playlist <id> [shuffle]` | Play a playlist by its base62 ID, optionally with shuffle |
| `queue <uri>` | Add a track to the playback queue |

#### Device Management

| Command | Description |
|---------|-------------|
| `devices` | List devices from the cached cluster state (instant, no network) |
| `apidevices` | List devices from the Spotify Web API |
| `transfer <device_id>` | Transfer playback to another device |
| `select` | Interactively choose a target device for subsequent commands |

#### State & Metadata

| Command | Description |
|---------|-------------|
| `state` | Show the current player state (track, position, shuffle, repeat, etc.) |
| `metadata` | Show cached metadata for the currently playing track |
| `fetchmeta [uri]` | Fetch metadata from the private API for the current track or a specified URI |

#### Event Watching

| Command | Description |
|---------|-------------|
| `watch` | Toggle live event watching — prints device, playback, and metadata events as they arrive |

#### Other

| Command | Description |
|---------|-------------|
| `help` | Show all available commands |
| `quit` / `exit` / `q` | Exit the application |

### Micro-Controller Code Walkthrough

The micro-controller is a single `main.go` file that follows this structure:

#### 1. Connection Setup

The application uses `quick.Connect()` for the simplest possible setup:

```go
result, err := quick.Connect(ctx, quick.QuickConfig{
    StatePath:    *statePath,
    DeviceName:   *deviceName,
    DeviceType:   spotcontrol.DeviceTypeComputer,
    Interactive:  *interactive,
    CallbackPort: *callbackPort,
})
```

This single call handles state loading, credential selection, session creation, controller startup, and state persistence.

#### 2. Event Subscription

The application subscribes to all three event channels immediately after connecting:

```go
deviceCh := ctrl.SubscribeDeviceList()
playbackCh := ctrl.SubscribePlayback()
metaCh := ctrl.SubscribeMetadata()
```

A background goroutine reads from these channels and prints events when the `watch` toggle is enabled:

```go
var watching atomic.Bool

go func() {
    for {
        select {
        case evt, ok := <-deviceCh:
            if !ok { return }
            if watching.Load() {
                // Print device event
            }
        case evt, ok := <-playbackCh:
            // ...
        case evt, ok := <-metaCh:
            // ...
        }
    }
}()
```

This pattern demonstrates how to consume events in the background without blocking the main command loop.

#### 3. Command Loop

The main loop reads lines from stdin, parses them into commands, and dispatches to the appropriate controller method:

```go
for {
    fmt.Print(">>> ")
    text, err := reader.ReadString('\n')
    if err != nil { break }
    cmds := strings.Fields(strings.TrimSpace(text))
    if len(cmds) == 0 { continue }

    switch cmds[0] {
    case "play":
        err := ctrl.Play(ctx, ident)
        // ...
    case "pause":
        err := ctrl.Pause(ctx, ident)
        // ...
    // ... more commands
    }
}
```

The `ident` variable holds the selected target device ID (set by the `select` command). An empty string targets the active device.

#### 4. URL Parsing

The `open` command demonstrates parsing Spotify web URLs into actionable URIs:

```go
entityType, entityId := parseSpotifyURL(cmds[1])
// Handles: https://open.spotify.com/track/2AX9H0uIFZqo9zAcwclQy9?si=abc123
//          https://open.spotify.com/playlist/5ese9XhQqKHoQg4WJ4sZef
//          https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy
//          https://open.spotify.com/intl-en/track/... (locale prefix)
```

Based on the entity type, different controller methods are used:
- **track** → `PlayTrack()` (connect-state, no context)
- **playlist** → `PlayPlaylist()` (connect-state, with playlist context)
- **album** → `LoadTrack()` with `ContextURI` (Web API style)

#### 5. Device Selection

The `select` command demonstrates interactive device selection from the cluster cache:

```go
func chooseDevice(ctrl *controller.Controller, reader *bufio.Reader) string {
    devices := ctrl.ListDevices()
    // Display numbered list
    // Read user's choice
    // Return device ID
}
```

### Example Session

```
$ ./micro-controller --interactive
Connected as: johnsmith
Device ID: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2

Available commands:
  open <url>                  : play a Spotify URL (track, playlist, or album)
  load <uri1> [uri2 ...]      : load track(s) via Web API
  ...

>>> devices

Devices (from cluster):
  - Living Room Speaker (SPEAKER) id=abc123 vol=45% [ACTIVE]
  - Desktop (COMPUTER) id=def456 vol=100%

>>> open https://open.spotify.com/track/5TFCp6cxCaJRhbdn6IWEGh
Playing track 5TFCp6cxCaJRhbdn6IWEGh

>>> metadata

Track Metadata (cached):
  Title:    Koopa's Theme (From "Super Mario 64")
  Artist:   Qumu
  Album:    Year 6
  Duration: 180000ms
  URI:      spotify:track:5TFCp6cxCaJRhbdn6IWEGh
  Image:    https://i.scdn.co/image/ab67616d0000b27340baf4dd6b252ba0943afdc9

>>> volume 30
Volume set to 30%

>>> watch
Live event watching: ON (device/playback/metadata events will print as they arrive)

>>> next
Skipped to next

[EVENT] Playback: playing | spotify:track:3n3Ppam7vgaVa1iaRUc9Lp | 0ms/210000ms | shuffle=false repeat_ctx=false repeat_trk=false

[EVENT] Metadata: "Another Track" by "Another Artist" on "Some Album" (210000ms)
        Art: https://i.scdn.co/image/...

>>> state

Player State:
  Playing:  true
  Track:    spotify:track:3n3Ppam7vgaVa1iaRUc9Lp
  Context:  spotify:album:4aawyAB9vmqN3uQ7FjRGTy
  Position: 2345ms / 210000ms
  Device:   abc123
  Shuffle:  false
  Repeat:   context=false track=false

>>> playlist 5ese9XhQqKHoQg4WJ4sZef shuffle
Playing playlist 5ese9XhQqKHoQg4WJ4sZef (shuffle on)

>>> transfer def456
Transferred playback

>>> quit
Goodbye!
```

---

## event-watcher

**Location:** `examples/event-watcher/`

### Event-Watcher Overview

The event-watcher is a minimal, non-interactive application that subscribes to all three event channels and prints events as they arrive in real time. It demonstrates:

- Connecting to Spotify with `quick.Connect()`
- Subscribing to device list, playback, and metadata events
- Using a `select` loop to process multiple event channels
- Graceful shutdown on SIGINT/SIGTERM

This example is useful for monitoring what's happening on your Spotify account — which devices are online, what's playing, and when tracks change.

### Building Event-Watcher

```sh
cd examples/event-watcher
go build -o event-watcher
```

Or run directly:

```sh
go run ./examples/event-watcher
```

### Running Event-Watcher

#### First Run (Interactive Login)

```sh
./event-watcher --interactive
```

#### Subsequent Runs

```sh
./event-watcher
```

The event-watcher has no interactive commands — it simply connects and prints events until you press Ctrl+C.

### Event-Watcher Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--interactive` | `false` | Use interactive OAuth2 PKCE login flow |
| `--state` | `spotcontrol_state.json` | Path to state file |
| `--devicename` | `EventWatcher` | Name shown in Spotify Connect device lists |
| `--port` | `0` | OAuth2 callback port (`0` = random) |

### Event-Watcher Code Walkthrough

The event-watcher is intentionally simple — about 120 lines of code in a single `main.go`.

#### 1. Connection

Uses `quick.Connect()` — identical pattern to the micro-controller:

```go
result, err := quick.Connect(ctx, quick.QuickConfig{
    StatePath:    *statePath,
    DeviceName:   *deviceName,
    DeviceType:   spotcontrol.DeviceTypeComputer,
    Interactive:  *interactive,
    CallbackPort: *callbackPort,
})
```

#### 2. Event Subscription

Subscribes to all three event channels:

```go
deviceCh := result.SubscribeDeviceList()
playbackCh := result.SubscribePlayback()
metadataCh := result.SubscribeMetadata()
```

#### 3. Event Loop

The main loop uses a `select` statement to process events from all three channels plus context cancellation:

```go
for {
    select {
    case <-ctx.Done():
        fmt.Println("Goodbye!")
        return

    case evt, ok := <-deviceCh:
        if !ok { return }
        // Print device list change

    case evt, ok := <-playbackCh:
        if !ok { return }
        // Print playback state change

    case evt, ok := <-metadataCh:
        if !ok { return }
        // Print track metadata
    }
}
```

This is the canonical pattern for consuming spotcontrol events. Key points:

- **Check `ok`**: When the controller closes (e.g. on shutdown), all channels are closed. The two-value receive detects this.
- **Non-blocking**: The `select` statement only blocks when no events are available. It doesn't poll or sleep.
- **Graceful shutdown**: The `ctx.Done()` case handles SIGINT/SIGTERM signals.

#### 4. Signal Handling

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
go func() {
    <-sigCh
    fmt.Println("\nShutting down...")
    cancel()
}()
```

When Ctrl+C is pressed, the context is cancelled, which:
1. Triggers `ctx.Done()` in the event loop
2. The loop exits
3. `result.Close()` (deferred) shuts down the controller and session
4. All event channels are closed

#### 5. Event Formatting

Each event type is printed with rich formatting:

**Device list events** show all devices with their names, types, volume levels, and active status:

```
[DEVICES] Changed (reason=NEW_DEVICE_APPEARED)
  Triggered by: [abc123]
  • Living Room Speaker (SPEAKER) vol=45% [ACTIVE]
  • Desktop (COMPUTER) vol=100%
```

**Playback events** show the play/pause status, track URI, position, and player options:

```
[PLAYBACK] ▶ Playing
  Track:    spotify:track:5TFCp6cxCaJRhbdn6IWEGh
  Context:  spotify:album:1709f7cb31ef4a9cb727cc12e1d3dbce
  Position: 0:12 / 3:00
  Device:   abc123
  Shuffle:  false  Repeat: context=false track=false
```

**Metadata events** show the rich track information fetched from the private metadata API:

```
[METADATA] 🎵 Koopa's Theme (From "Super Mario 64")
  Artist:   Qumu
  Album:    Year 6
  Duration: 3:00
  URI:      spotify:track:5TFCp6cxCaJRhbdn6IWEGh
  Cover:    https://i.scdn.co/image/ab67616d0000b27340baf4dd6b252ba0943afdc9
```

### Example Output

```
$ ./event-watcher
Connecting to Spotify...
Connected as: johnsmith
Device ID:    a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2

Listening for events (Ctrl+C to quit)...
---

[DEVICES] Changed (reason=NEW_DEVICE_APPEARED)
  • Living Room Speaker (SPEAKER) vol=45% [ACTIVE]
  • Desktop (COMPUTER) vol=100%

[PLAYBACK] ▶ Playing
  Track:    spotify:track:5TFCp6cxCaJRhbdn6IWEGh
  Context:  spotify:album:1709f7cb31ef4a9cb727cc12e1d3dbce
  Position: 0:12 / 3:00
  Device:   abc123
  Shuffle:  false  Repeat: context=false track=false

[METADATA] 🎵 Koopa's Theme (From "Super Mario 64")
  Artist:   Qumu
  Album:    Year 6
  Duration: 3:00
  URI:      spotify:track:5TFCp6cxCaJRhbdn6IWEGh
  Cover:    https://i.scdn.co/image/ab67616d0000b27340baf4dd6b252ba0943afdc9

[PLAYBACK] ⏸ Paused
  Track:    spotify:track:5TFCp6cxCaJRhbdn6IWEGh
  Position: 1:34 / 3:00
  Device:   abc123
  Shuffle:  false  Repeat: context=false track=false

[DEVICES] Changed (reason=DEVICES_DISAPPEARED)
  • Living Room Speaker (SPEAKER) vol=45% [ACTIVE]

^C
Shutting down...
Goodbye!
```

---

## Writing Your Own Application

Both examples follow the same fundamental pattern. Here's the minimal template:

### Minimal Application

```go
package main

import (
    "context"
    "fmt"
    "log"

    spotcontrol "github.com/mcMineyC/spotcontrol"
    "github.com/mcMineyC/spotcontrol/quick"
)

func main() {
    ctx := context.Background()

    result, err := quick.Connect(ctx, quick.QuickConfig{
        StatePath:   "state.json",
        DeviceName:  "MyApp",
        Interactive: true,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer result.Close()

    fmt.Printf("Connected as %s\n", result.Session.Username())

    // Your code here — use result.Play(), result.Pause(), etc.
}
```

### Key Patterns

#### Pattern 1: Fire-and-Forget Commands

```go
// Simple one-shot playback control
result.Play(ctx, "")
result.SetVolume(ctx, 50, "")
result.Next(ctx, "")
```

#### Pattern 2: Event-Driven Processing

```go
playbackCh := result.SubscribePlayback()
metaCh := result.SubscribeMetadata()

for {
    select {
    case evt := <-playbackCh:
        // React to playback changes
        updateUI(evt.State)
    case evt := <-metaCh:
        // React to track changes
        displayCoverArt(evt.Metadata.ImageURL)
    }
}
```

#### Pattern 3: Polling State

```go
// Periodic state check (prefer events when possible)
ticker := time.NewTicker(5 * time.Second)
for range ticker.C {
    state, err := result.GetPlayerState(ctx)
    if err != nil { continue }
    if state != nil && state.IsPlaying {
        fmt.Printf("Playing: %s at %dms\n", state.TrackURI, state.PositionMs)
    }
}
```

#### Pattern 4: Manual Session + Controller

For more control, skip `quick.Connect()` and manage components directly:

```go
import (
    "github.com/mcMineyC/spotcontrol/session"
    "github.com/mcMineyC/spotcontrol/controller"
)

// Load state
state, _ := spotcontrol.LoadState("state.json")

// Create session
sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    DeviceType:  spotcontrol.DeviceTypeComputer,
    DeviceName:  "MyApp",
    DeviceId:    state.DeviceId,
    Credentials: session.StoredCredentials{
        Username: state.Username,
        Data:     state.StoredCredentials,
    },
    AppState: state,
})

// Create and start controller
ctrl := sess.NewController()
ctrl.Start(ctx)

// Save updated state
spotcontrol.SaveState("state.json", sess.ExportState())
```

### Choosing Between Examples

| Use Case | Start From |
|----------|-----------|
| Interactive CLI tool | micro-controller |
| Background event monitor / daemon | event-watcher |
| Web server integration | event-watcher pattern (event-driven) |
| Simple script (play a track and exit) | Minimal template above |
| Custom UI (desktop/mobile) | Manual session + controller pattern |

---

## Next Steps

- **[Getting Started](getting-started.md)** — Installation and quick start guide
- **[Controller Guide](controller-guide.md)** — Deep dive into the controller API
- **[Configuration](configuration.md)** — All configuration options
- **[Authentication](authentication.md)** — Authentication methods and credential management