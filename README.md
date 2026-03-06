# spotcontrol

Spotcontrol is a Go library for controlling [Spotify Connect](https://www.spotify.com/connect/) devices. It implements Spotify's modern Connect protocol stack including access point (AP) authentication, Login5 token management, dealer WebSocket real-time messaging, spclient HTTP API, and Connect State device control.

This is a modernized rewrite based on the protocol details from [go-librespot](https://github.com/devgianlu/go-librespot) and the original [librespot](https://github.com/librespot-org/librespot) project. Spotcontrol focuses solely on **remote control** of other Spotify devices — it does not play music itself.

## Features

- **Access Point (AP) Protocol** — Diffie-Hellman key exchange, Shannon stream cipher encryption, automatic reconnection with backoff
- **Login5 Authentication** — Modern token-based auth with automatic hashcash challenge solving and token renewal
- **Client Token** — Automatic retrieval from `clienttoken.spotify.com`
- **AP Resolver** — Discovers and caches `accesspoint`, `spclient`, and `dealer` endpoints via `apresolve.spotify.com`
- **Dealer WebSocket** — Real-time push notifications for Connect State cluster updates, ping/pong keepalive, automatic reconnection
- **Spclient HTTP API** — Connect State management (`PUT /connect-state/v1/devices/...`) and Spotify Web API proxying with automatic bearer token injection and retry logic
- **Mercury (Hermes)** — Pub/sub messaging over the AP connection for legacy protocol support
- **Controller** — High-level API for listing devices, play/pause/next/previous, volume, seek, shuffle, repeat, track loading, playback transfer, and queue management
- **Multiple Auth Methods** — Stored credentials, OAuth2 PKCE interactive login, Spotify tokens, and encrypted discovery blobs
- **Session Orchestration** — Single `Session` object wires together all components with a clean lifecycle
- **Convenience Helpers** — `quick.Connect()` one-liner, automatic device ID generation, JSON state persistence, built-in loggers

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   Session                        │
│  (orchestrates all components, manages auth)     │
├──────────┬──────────┬───────────┬───────────────┤
│    AP    │  Login5  │ Spclient  │    Dealer      │
│  (TCP)   │ (HTTPS)  │  (HTTPS)  │ (WebSocket)    │
├──────────┤          │           │               │
│ Mercury  │          │           │               │
│ (pub/sub)│          │           │               │
└──────────┴──────────┴───────────┴───────────────┘

┌─────────────────────────────────────────────────┐
│               Controller                         │
│  (high-level device control via Web API)         │
│  Uses: Spclient, Dealer                          │
└─────────────────────────────────────────────────┘
```

## Installation

```
go get github.com/mcMineyC/spotcontrol
```

### Protobuf Code Generation

The repository includes `.proto` source files under `proto/spotify/` and pre-generated `*.pb.go` files. If you need to regenerate the protobuf Go code (e.g. after modifying `.proto` files):

1. Install [buf](https://buf.build/docs/installation) and the Go protobuf plugin:

   ```sh
   go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
   ```

   > **Note:** Use `protoc-gen-go` v1.34.x to generate files with `var rawDesc = []byte{...}` literals. Newer versions (v1.36+) generate `const rawDesc string` with `unsafe.StringData`, which can cause panics with some Go toolchain versions.

2. Generate from the `proto/` directory:

   ```sh
   cd proto
   buf generate
   ```

   This produces `*.pb.go` files alongside their corresponding `.proto` sources using `paths=source_relative`.

## Quick Start

### One-liner with `quick.Connect()`

The simplest way to get started — handles session creation, authentication, state persistence, and controller setup in a single call:

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

    // Play a track on the active device.
    if err := result.Play(ctx, ""); err != nil {
        log.Fatal(err)
    }
}
```

On the first run, `quick.Connect()` opens an OAuth2 PKCE flow (prints a URL for the user to visit). Credentials are automatically saved to `spotcontrol_state.json`, so subsequent runs authenticate silently.

### Advanced: Manual Session + Controller

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
        Log:        spotcontrol.NewSimpleLogger(nil), // or NewSlogLogger, or your own Logger
        DeviceType: spotcontrol.DeviceTypeComputer,    // re-exported from protobuf for convenience
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
    ctrl := sess.NewController() // convenience method, or use controller.NewController(cfg)
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

### Using Stored Credentials

```go
// Load previously saved state.
state, _ := spotcontrol.LoadState("state.json")

sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    DeviceType: spotcontrol.DeviceTypeComputer,
    DeviceId:   state.DeviceId,
    DeviceName: "MyApp",
    Credentials: session.StoredCredentials{
        Username: state.Username,
        Data:     state.StoredCredentials,
    },
    AppState: state, // restores OAuth2 token for Web API access
})
```

## Convenience Helpers

The root `spotcontrol` package provides several helpers to reduce boilerplate:

| Helper | Description |
|--------|-------------|
| `GenerateDeviceId()` | Generates a random 40-hex-char device ID (crypto/rand) |
| `LoadState(path)` | Loads `AppState` from JSON; returns `(nil, nil)` if file doesn't exist |
| `SaveState(path, state)` | Saves `AppState` as JSON with `0600` permissions |
| `NewSimpleLogger(w)` | Ready-made `Logger` that writes to an `io.Writer` (suppresses Trace) |
| `NewSlogLogger(l)` | Adapts `*slog.Logger` to the `Logger` interface |
| `DeviceTypeComputer`, etc. | Re-exported device type constants (no protobuf import needed) |

The `session.Session` type also provides:

| Method | Description |
|--------|-------------|
| `ExportState()` | Builds an `AppState` from the session (device ID, username, credentials, OAuth2 token) |
| `NewController()` | Creates a `controller.Controller` pre-configured from the session |

## Example CLI

A complete interactive CLI is included in `examples/micro-controller/`:

```sh
cd examples/micro-controller
go build -o micro-controller

# First run — interactive OAuth2 login (saves credentials automatically):
./micro-controller --interactive

# Subsequent runs — uses saved credentials:
./micro-controller

# With a custom device name:
./micro-controller --devicename "My Speaker"
```

Available commands in the CLI:

| Command | Description |
|---------|-------------|
| `load <uri> [uri...]` | Load and play track(s) by Spotify URI |
| `play` | Resume playback |
| `pause` | Pause playback |
| `next` / `prev` | Skip forward / backward |
| `volume <0-100>` | Set volume percentage |
| `seek <ms>` | Seek to position in milliseconds |
| `shuffle <on\|off>` | Toggle shuffle |
| `repeat <off\|context\|track>` | Set repeat mode |
| `queue <uri>` | Add track to queue |
| `transfer <device_id>` | Transfer playback to another device |
| `devices` | List devices from cluster state |
| `apidevices` | List devices from Web API |
| `state` | Show current player state |

## Package Overview

| Package | Description |
|---------|-------------|
| `spotcontrol` | Root package with shared types (`Logger`, `AppState`, `GetAddressFunc`), convenience helpers (`GenerateDeviceId`, `LoadState`/`SaveState`, `NewSimpleLogger`/`NewSlogLogger`), ID utilities, platform detection, version info |
| `quick` | High-level convenience constructor — `quick.Connect()` wires together state persistence, authentication, session, and controller in one call |
| `session` | Session orchestrator — wires AP, Login5, spclient, dealer, and Mercury together; handles OAuth2 PKCE flow; provides `ExportState()` and `NewController()` |
| `controller` | High-level playback control API — device listing, play/pause/skip, volume, shuffle, repeat, track loading, transfer, queue |
| `ap` | Access point TCP connection — DH key exchange, Shannon encryption, packet framing, ping/pong, reconnection |
| `apresolve` | Service endpoint resolver (`apresolve.spotify.com`) for AP, dealer, and spclient addresses |
| `dealer` | Dealer WebSocket client — real-time push messages, cluster updates, request/reply protocol |
| `dh` | Diffie-Hellman key exchange using Spotify's 768-bit MODP group |
| `login5` | Login5 authentication client with hashcash challenge solver and automatic token renewal |
| `mercury` | Mercury (Hermes) pub/sub messaging over AP packets |
| `spclient` | HTTP client for Spotify's spclient API — Connect State PUT, Web API proxy, automatic auth token injection |
| `proto/` | Protobuf definitions and generated Go code for all Spotify protocol messages |

## Protocol Reference

This library implements the following Spotify protocols:

- **AP (Access Point)**: TCP connection with DH key exchange → Shannon stream cipher → framed encrypted packets. Handles `Login`, `APWelcome`, `Ping/Pong`, Mercury, and other packet types.
- **Login5**: HTTPS POST to `login5.spotify.com/v3/login` with protobuf request/response. Supports hashcash challenges for rate limiting.
- **Client Token**: HTTPS POST to `clienttoken.spotify.com/v1/clienttoken` with platform-specific device info.
- **Dealer**: WebSocket connection to `dealer.spotify.com` with JSON-framed messages. Receives Connect State cluster updates and control requests.
- **Spclient**: HTTPS API for `PUT /connect-state/v1/devices/{id}` (register device, update state) and proxied Web API calls.
- **Connect State**: Protobuf-based device cluster management replacing the legacy SPIRC protocol. Devices register with `PutStateRequest` and receive `ClusterUpdate` pushes via the dealer.

## Testing

```sh
go test ./...
```

## License

See [LICENSE](LICENSE).

## DISCLAIMER
Much of this code was written with the use of Claude Opus 4.6