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
go get github.com/badfortrains/spotcontrol
```

### Protobuf Code Generation

The repository includes `.proto` source files under `proto/spotify/` and pre-generated `*.pb.go` files. If you need to regenerate the protobuf Go code (e.g. after modifying `.proto` files):

1. Install [buf](https://buf.build/docs/installation) and the Go protobuf plugin:

   ```sh
   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
   ```

2. Generate from the `proto/` directory:

   ```sh
   cd proto
   buf generate
   ```

   This produces `*.pb.go` files alongside their corresponding `.proto` sources using `paths=source_relative`.

## Quick Start

### Interactive OAuth2 Login

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/badfortrains/spotcontrol/controller"
    devicespb "github.com/badfortrains/spotcontrol/proto/spotify/connectstate/devices"
    "github.com/badfortrains/spotcontrol/session"
)

func main() {
    ctx := context.Background()

    // Create a session with interactive OAuth2 PKCE login.
    // This opens a browser for the user to authenticate.
    sess, err := session.NewSessionFromOptions(ctx, &session.Options{
        DeviceType: devicespb.DeviceType_COMPUTER,
        DeviceId:   "your-40-hex-char-device-id-here000000000",
        DeviceName: "MyApp",
        Credentials: session.InteractiveCredentials{
            CallbackPort: 0, // random port
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer sess.Close()

    // Save credentials for next time.
    fmt.Printf("Username: %s\n", sess.Username())
    fmt.Printf("Stored credentials (save these): %x\n", sess.StoredCredentials())

    // Create a controller for device management.
    ctrl := controller.NewController(controller.Config{
        Spclient:   sess.Spclient(),
        Dealer:     sess.Dealer(),
        DeviceId:   sess.DeviceId(),
        DeviceName: "MyApp",
        DeviceType: devicespb.DeviceType_COMPUTER,
    })
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
        fmt.Printf("Device: %s (%s) active=%v\n", d.Name, d.Type, d.IsActive)
    }

    // Play a track on the active device.
    err = ctrl.LoadTrack(ctx, []string{"spotify:track:6rqhFgbbKwnb9MLmUQDhG6"}, nil)
    if err != nil {
        log.Fatal(err)
    }
}
```

### Using Stored Credentials

```go
sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    DeviceType: devicespb.DeviceType_COMPUTER,
    DeviceId:   "your-40-hex-char-device-id-here000000000",
    DeviceName: "MyApp",
    Credentials: session.StoredCredentials{
        Username: savedUsername,
        Data:     savedCredentialBytes,
    },
})
```

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
| `spotcontrol` | Root package with shared types (`Logger`, `AppState`, `GetAddressFunc`), ID utilities, platform detection, version info |
| `ap` | Access point TCP connection — DH key exchange, Shannon encryption, packet framing, ping/pong, reconnection |
| `apresolve` | Service endpoint resolver (`apresolve.spotify.com`) for AP, dealer, and spclient addresses |
| `controller` | High-level playback control API — device listing, play/pause/skip, volume, shuffle, repeat, track loading, transfer |
| `dealer` | Dealer WebSocket client — real-time push messages, cluster updates, request/reply protocol |
| `dh` | Diffie-Hellman key exchange using Spotify's 768-bit MODP group |
| `login5` | Login5 authentication client with hashcash challenge solver and automatic token renewal |
| `mercury` | Mercury (Hermes) pub/sub messaging over AP packets |
| `session` | Session orchestrator — wires AP, Login5, spclient, dealer, and Mercury together; handles OAuth2 PKCE flow |
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