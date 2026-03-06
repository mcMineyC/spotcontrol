# Package Reference

This document provides detailed API documentation for every package in the spotcontrol library. Each section covers the package's purpose, exported types, functions, and usage patterns.

## Table of Contents

- [Root Package (`spotcontrol`)](#root-package-spotcontrol)
- [`session`](#session)
- [`controller`](#controller)
- [`quick`](#quick)
- [`ap` (Access Point)](#ap-access-point)
- [`dh` (Diffie-Hellman)](#dh-diffie-hellman)
- [`login5`](#login5)
- [`apresolve`](#apresolve)
- [`dealer`](#dealer)
- [`spclient`](#spclient)
- [`mercury`](#mercury)

---

## Root Package (`spotcontrol`)

**Import**: `github.com/mcMineyC/spotcontrol`

The root package provides shared types, constants, utilities, logging, state persistence, and ID conversion functions used throughout the library.

### Types

#### `DeviceType`

A type alias for `devicespb.DeviceType` (the protobuf enum). Re-exported constants let you avoid importing the deeply nested protobuf package:

| Constant | Protobuf Equivalent |
|----------|---------------------|
| `DeviceTypeComputer` | `DeviceType_COMPUTER` |
| `DeviceTypeTablet` | `DeviceType_TABLET` |
| `DeviceTypeSmartphone` | `DeviceType_SMARTPHONE` |
| `DeviceTypeSpeaker` | `DeviceType_SPEAKER` |
| `DeviceTypeTV` | `DeviceType_TV` |
| `DeviceTypeAVR` | `DeviceType_AVR` |
| `DeviceTypeSTB` | `DeviceType_STB` |
| `DeviceTypeAudioDongle` | `DeviceType_AUDIO_DONGLE` |
| `DeviceTypeGameConsole` | `DeviceType_GAME_CONSOLE` |
| `DeviceTypeCastVideo` | `DeviceType_CAST_VIDEO` |
| `DeviceTypeCastAudio` | `DeviceType_CAST_AUDIO` |
| `DeviceTypeAutomobile` | `DeviceType_AUTOMOBILE` |
| `DeviceTypeSmartwatch` | `DeviceType_SMARTWATCH` |
| `DeviceTypeChromebook` | `DeviceType_CHROMEBOOK` |
| `DeviceTypeCarThing` | `DeviceType_CAR_THING` |

#### `AppState`

Holds persisted state across sessions:

```go
type AppState struct {
    DeviceId          string    `json:"device_id"`
    Username          string    `json:"username,omitempty"`
    StoredCredentials []byte    `json:"stored_credentials,omitempty"`
    OAuthAccessToken  string    `json:"oauth_access_token,omitempty"`
    OAuthRefreshToken string    `json:"oauth_refresh_token,omitempty"`
    OAuthTokenType    string    `json:"oauth_token_type,omitempty"`
    OAuthExpiry       time.Time `json:"oauth_expiry,omitempty"`
}
```

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `HasOAuthToken` | `() bool` | Returns `true` if an OAuth2 access or refresh token is present |

#### `SpotifyId`

Represents a Spotify resource identifier (type + 16-byte GID).

**Methods:**

| Method | Returns | Description |
|--------|---------|-------------|
| `Type()` | `SpotifyIdType` | The resource type (`"track"`, `"episode"`, `"album"`, etc.) |
| `Id()` | `[]byte` | The raw 16-byte identifier |
| `Hex()` | `string` | Hex-encoded GID |
| `Base62()` | `string` | Base62-encoded ID (22 chars, zero-padded) |
| `Uri()` | `string` | Full URI, e.g. `"spotify:track:6rqhFgbbKwnb9MLmUQDhG6"` |
| `String()` | `string` | Same as `Uri()` |

#### `SpotifyIdType`

String constants for resource types:

| Constant | Value |
|----------|-------|
| `SpotifyIdTypeTrack` | `"track"` |
| `SpotifyIdTypeEpisode` | `"episode"` |
| `SpotifyIdTypeAlbum` | `"album"` |
| `SpotifyIdTypeArtist` | `"artist"` |
| `SpotifyIdTypePlaylist` | `"playlist"` |
| `SpotifyIdTypeShow` | `"show"` |

#### `GetAddressFunc`

```go
type GetAddressFunc func(ctx context.Context) string
```

A function that returns an endpoint address, rotating through available addresses on each call.

#### `GetLogin5TokenFunc`

```go
type GetLogin5TokenFunc func(ctx context.Context, force bool) (string, error)
```

A function that returns a bearer token. When `force` is `true`, a fresh token is obtained even if the cached one hasn't expired.

#### `Logger`

Interface for structured logging, compatible with logrus-style loggers:

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

### Functions

#### State Persistence

| Function | Signature | Description |
|----------|-----------|-------------|
| `LoadState` | `(path string) (*AppState, error)` | Reads `AppState` from a JSON file. Returns `(nil, nil)` if the file doesn't exist. |
| `SaveState` | `(path string, state *AppState) error` | Writes `AppState` as pretty-printed JSON with `0600` permissions. |

#### ID Conversion

| Function | Signature | Description |
|----------|-----------|-------------|
| `SpotifyIdFromUri` | `(uri string) (*SpotifyId, error)` | Parses a Spotify URI (e.g. `"spotify:track:..."`) |
| `SpotifyIdFromBase62` | `(typ SpotifyIdType, id string) (*SpotifyId, error)` | Creates a `SpotifyId` from type + base62 string |
| `SpotifyIdFromGid` | `(typ SpotifyIdType, id []byte) SpotifyId` | Creates a `SpotifyId` from type + raw 16-byte GID |
| `GidToBase62` | `(id []byte) string` | Converts raw GID bytes to base62 (22 chars) |
| `Base62ToGid` | `(id string) ([]byte, error)` | Converts base62 to raw GID bytes |
| `InferSpotifyIdTypeFromContextUri` | `(uri string) SpotifyIdType` | Infers whether a context URI is episode/show or track content |

#### Device Utilities

| Function | Signature | Description |
|----------|-----------|-------------|
| `GenerateDeviceId` | `() string` | Generates a random 40-hex-char device ID (20 bytes from `crypto/rand`) |

#### Logging Constructors

| Function | Signature | Description |
|----------|-----------|-------------|
| `NewSimpleLogger` | `(w io.Writer) Logger` | Human-readable log lines to `w` (default `os.Stderr` if nil). Trace suppressed. |
| `NewSlogLogger` | `(l *slog.Logger) Logger` | Adapts Go's `log/slog.Logger` to the `Logger` interface |
| `NullLogger{}` | (struct) | Discards all output |

#### Version & Identity

| Function | Returns | Description |
|----------|---------|-------------|
| `VersionNumberString()` | `string` | Version number (from ldflags, commit hash, or `"dev"`) |
| `VersionString()` | `string` | `"spotcontrol <version>"` |
| `UserAgent()` | `string` | HTTP User-Agent string |
| `SystemInfoString()` | `string` | System info for protobuf fields |
| `ObfuscateUsername(username)` | `string` | First 3 chars + `"***"` for safe logging |

#### Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `SpotifyVersionCode` | `127700358` | Version code sent during AP key exchange |
| `ClientIdHex` | `"65b708073fc0480ea92a077233ca87bd"` | Well-known Spotify client ID (hex) |
| `ClientId` | `[]byte{...}` | Raw 16-byte client ID |

---

## `session`

**Import**: `github.com/mcMineyC/spotcontrol/session`

The `session` package orchestrates the full connection lifecycle: client token retrieval, AP resolution, AP connection, Login5 authentication, spclient initialization, dealer WebSocket setup, and Mercury pub/sub.

### `Session`

The main type. Created via `NewSessionFromOptions`.

**Constructor:**

```go
func NewSessionFromOptions(ctx context.Context, opts *Options) (*Session, error)
```

Performs the full connection and authentication flow (8 steps):
1. Retrieve client token
2. Resolve endpoints via apresolve
3. Create Login5 client
4. Connect and authenticate with access point (AP)
5. Authenticate with Login5 using stored credentials from AP
6. Initialize spclient HTTP wrapper
7. Initialize dealer WebSocket client
8. Initialize Mercury pub/sub client

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Close()` | | Shuts down all connections and background goroutines |
| `Accesspoint()` | `*ap.Accesspoint` | Returns the underlying AP connection |
| `Spclient()` | `*spclient.Spclient` | Returns the spclient HTTP wrapper |
| `Dealer()` | `*dealer.Dealer` | Returns the dealer WebSocket client |
| `Mercury()` | `*mercury.Client` | Returns the Mercury pub/sub client |
| `Login5Client()` | `*login5.Login5` | Returns the Login5 authentication client |
| `Resolver()` | `*apresolve.ApResolver` | Returns the AP resolver |
| `Username()` | `string` | Canonical username |
| `StoredCredentials()` | `[]byte` | Reusable auth credential bytes from AP |
| `DeviceId()` | `string` | 40-hex-char device identifier |
| `DeviceName()` | `string` | Human-readable device name |
| `DeviceType()` | `devicespb.DeviceType` | Device type enum |
| `ClientToken()` | `string` | Client token string |
| `AccessToken(ctx, force)` | `(string, error)` | Login5 bearer token (auto-renews) |
| `WebApiToken()` | `GetLogin5TokenFunc` | OAuth2 token function for Web API (auto-refreshes) |
| `OAuthToken()` | `*oauth2.Token` | Current OAuth2 token or nil |
| `ExportState()` | `*AppState` | Captures session state for persistence |
| `NewController()` | `*controller.Controller` | Creates a pre-configured controller |

### `Options`

```go
type Options struct {
    Log         spotcontrol.Logger
    DeviceType  devicespb.DeviceType  // Required
    DeviceId    string                // Auto-generated if empty (must be 40 hex chars if set)
    DeviceName  string                // Default: "SpotControl"
    Credentials Credentials           // Required (one of the four types below)
    ClientToken string                // Auto-fetched if empty
    Resolver    *apresolve.ApResolver // Auto-created if nil
    Client      *http.Client          // Default: 30s timeout
    AppState    *spotcontrol.AppState // Optional; restores OAuth2 token if present
}
```

### Credential Types

All implement the `Credentials` marker interface.

#### `StoredCredentials`
```go
type StoredCredentials struct {
    Username string
    Data     []byte  // From APWelcome.ReusableAuthCredentials
}
```

#### `SpotifyTokenCredentials`
```go
type SpotifyTokenCredentials struct {
    Username string
    Token    string  // OAuth access token
}
```

#### `BlobCredentials`
```go
type BlobCredentials struct {
    Username string
    Blob     []byte  // Base64-encoded encrypted discovery blob
}
```

#### `InteractiveCredentials`
```go
type InteractiveCredentials struct {
    CallbackPort int  // 0 = random available port
}
```

---

## `controller`

**Import**: `github.com/mcMineyC/spotcontrol/controller`

The `controller` package provides the high-level public API for Spotify Connect device control. It maintains a cached view of the account's device cluster via dealer WebSocket push messages and provides methods for playback control, device management, and event subscriptions.

### `Controller`

The main type. Created via `NewController` or `Session.NewController()`.

**Constructor:**

```go
func NewController(cfg Config) *Controller
```

**Configuration:**

```go
type Config struct {
    Log            spotcontrol.Logger
    Spclient       *spclient.Spclient
    Dealer         *dealer.Dealer
    DeviceId       string
    DeviceName     string
    DeviceType     devicespb.DeviceType
    UseWebApi      bool           // Force Web API routing (not recommended)
    VolumeDebounce time.Duration  // Default: 500ms; negative = disabled
}
```

#### Lifecycle Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `Start(ctx)` | `error` | Connects dealer, subscribes to cluster updates, starts background processing |
| `RegisterDevice(ctx, connId)` | `error` | Announces device to connect-state backend (called automatically by `Start`) |
| `Close()` | | Stops background goroutines and closes subscriber channels |

#### Playback Control

All playback methods accept a `deviceId` string. Pass `""` to target the currently active device.

| Method | Signature | Description |
|--------|-----------|-------------|
| `Play(ctx, deviceId)` | `error` | Resume playback (connect-state "resume" command) |
| `Pause(ctx, deviceId)` | `error` | Pause playback (connect-state "pause" command) |
| `Next(ctx, deviceId)` | `error` | Skip to next track ("skip_next") |
| `Previous(ctx, deviceId)` | `error` | Skip to previous track ("skip_prev") |
| `Seek(ctx, positionMs, deviceId)` | `error` | Seek to position ("seek_to") |
| `SetVolume(ctx, volumePercent, deviceId)` | `error` | Set volume 0–100 (debounced, uses connect-state volume endpoint) |
| `SetShuffle(ctx, state, deviceId)` | `error` | Enable/disable shuffle ("set_shuffling_context") |
| `SetRepeat(ctx, state, deviceId)` | `error` | Set repeat: `"off"`, `"context"`, `"track"` |

#### Track Loading & Playback

| Method | Signature | Description |
|--------|-----------|-------------|
| `LoadTrack(ctx, trackURIs, opts)` | `error` | Play tracks via Web API PUT /v1/me/player/play |
| `PlayTrack(ctx, trackURIs, opts)` | `error` | Play tracks via connect-state (no context/recommendations) |
| `PlayPlaylist(ctx, playlistId, opts)` | `error` | Play a playlist by ID via connect-state |
| `AddToQueue(ctx, trackURI, deviceId)` | `error` | Add track to queue ("add_to_queue") |
| `TransferPlayback(ctx, deviceId, play)` | `error` | Transfer playback to another device |

**`LoadTrackOptions`:**

```go
type LoadTrackOptions struct {
    DeviceId       string
    ContextURI     string  // Album, playlist, or artist URI
    OffsetURI      string  // Track URI to start from within context
    OffsetPosition *int    // Zero-based index within context
    PositionMs     int64   // Start position within track
}
```

**`PlayTrackOptions`:**

```go
type PlayTrackOptions struct {
    DeviceId    string
    Shuffle     bool
    SkipToURI   string  // Track URI to start from
    SkipToIndex *int    // Zero-based index to start from
}
```

**`PlayPlaylistOptions`:**

```go
type PlayPlaylistOptions struct {
    DeviceId       string
    Shuffle        bool
    SkipToTrackURI string
    SkipToTrackUID string
}
```

#### Device Listing

| Method | Signature | Description |
|--------|-----------|-------------|
| `ListDevices()` | `[]DeviceInfo` | Devices from cached cluster (instant, no network) |
| `ListDevicesFromAPI(ctx)` | `([]DeviceInfo, error)` | Prefers cluster; falls back to Web API |
| `ListDevicesFromAPIForced(ctx)` | `([]DeviceInfo, error)` | Always queries Web API |
| `ActiveDeviceId()` | `string` | ID of the active device from cluster |
| `Cluster()` | `*connectpb.Cluster` | Raw cached cluster protobuf |

**`DeviceInfo`:**

```go
type DeviceInfo struct {
    Id             string
    Name           string
    Type           string  // e.g. "COMPUTER", "SPEAKER"
    IsActive       bool
    Volume         int     // 0–100 percentage
    SupportsVolume bool
}
```

#### Player State

| Method | Signature | Description |
|--------|-----------|-------------|
| `GetPlayerState(ctx)` | `(*PlayerState, error)` | Cluster first, Web API fallback |
| `GetPlayerStateFromCluster()` | `*PlayerState` | Instant read from cache |
| `GetPlayerStateFromAPI(ctx)` | `(*PlayerState, error)` | Always queries Web API |

**`PlayerState`:**

```go
type PlayerState struct {
    IsPlaying     bool
    TrackURI      string
    ContextURI    string
    PositionMs    int64   // Estimated current position (accounts for elapsed time)
    DurationMs    int64
    DeviceId      string
    Shuffle       bool
    RepeatContext bool
    RepeatTrack   bool
}
```

#### Event Subscriptions

| Method | Signature | Description |
|--------|-----------|-------------|
| `SubscribeDeviceList()` | `<-chan DeviceListEvent` | Device list changes |
| `SubscribePlayback()` | `<-chan PlaybackEvent` | Playback state changes |
| `SubscribeMetadata()` | `<-chan MetadataEvent` | Track metadata (fetched from private API) |

All channels are buffered (16 deep) and closed when the controller is closed. Events are dropped if a subscriber falls behind.

**`DeviceListEvent`:**

```go
type DeviceListEvent struct {
    Devices            []DeviceInfo
    DevicesThatChanged []string
    Reason             string  // e.g. "NEW_DEVICE_APPEARED", "DEVICES_DISAPPEARED"
}
```

**`PlaybackEvent`:**

```go
type PlaybackEvent struct {
    State PlayerState
}
```

**`MetadataEvent`:**

```go
type MetadataEvent struct {
    Metadata TrackMetadata
}
```

#### Track Metadata

| Method | Signature | Description |
|--------|-----------|-------------|
| `GetTrackMetadata()` | `*TrackMetadata` | Cached metadata for current track (instant, no network) |
| `FetchTrackMetadata(ctx, trackURI)` | `(*TrackMetadata, error)` | Fetch metadata for any track (network request) |
| `FetchCurrentTrackMetadata(ctx)` | `(*TrackMetadata, error)` | Fetch metadata for current track (network request) |

**`TrackMetadata`:**

```go
type TrackMetadata struct {
    TrackURI      string
    Title         string
    Artist        string  // Primary artist name
    Album         string
    DurationMs    int64
    ImageURL      string  // Largest cover art URL
    SmallImageURL string  // 64px cover art URL
    ArtistURI     string  // From cluster ProvidedTrack
    AlbumURI      string  // From cluster ProvidedTrack
    Raw           *spclient.TrackMetadata  // Full raw API response
}
```

---

## `quick`

**Import**: `github.com/mcMineyC/spotcontrol/quick`

The `quick` package provides the highest-level convenience API. A single `Connect()` call handles state loading, authentication, session creation, controller setup, and state persistence.

### `Connect`

```go
func Connect(ctx context.Context, cfg QuickConfig) (*ConnectResult, error)
```

**`QuickConfig`:**

```go
type QuickConfig struct {
    StatePath    string                  // Path to JSON state file (empty = no persistence)
    DeviceName   string                  // Default: "SpotControl"
    DeviceType   spotcontrol.DeviceType  // Default: DeviceTypeComputer
    DeviceId     string                  // Auto-generated if empty
    Interactive  bool                    // Enable OAuth2 PKCE when no stored credentials
    CallbackPort int                     // 0 = random port
    Log          spotcontrol.Logger      // Default: NewSimpleLogger(nil)
}
```

### `ConnectResult`

Wraps a `Session` and `Controller` with pass-through convenience methods for all common operations.

**Fields:**

```go
type ConnectResult struct {
    Session    *session.Session
    Controller *controller.Controller
}
```

**Methods** (all delegate to the underlying controller):

| Category | Methods |
|----------|---------|
| **Lifecycle** | `Close()` |
| **Playback** | `Play`, `Pause`, `Next`, `Previous`, `SetVolume`, `Seek`, `SetShuffle`, `SetRepeat` |
| **Track Loading** | `LoadTrack`, `PlayTrack`, `AddToQueue`, `TransferPlayback` |
| **Devices** | `ListDevices`, `ListDevicesFromAPI` |
| **State** | `GetPlayerState` |
| **Events** | `SubscribeDeviceList`, `SubscribePlayback`, `SubscribeMetadata` |
| **Metadata** | `GetTrackMetadata`, `FetchTrackMetadata`, `FetchCurrentTrackMetadata` |

### `ApplyDefaults`

```go
func ApplyDefaults(cfg QuickConfig) QuickConfig
```

Exported for testing — fills zero-valued `QuickConfig` fields with defaults.

---

## `ap` (Access Point)

**Import**: `github.com/mcMineyC/spotcontrol/ap`

The `ap` package manages the TCP connection to a Spotify access point. It handles the Diffie-Hellman key exchange, Shannon stream cipher encryption, authentication, and automatic reconnection.

### `Accesspoint`

**Constructor:**

```go
func NewAccesspoint(log spotcontrol.Logger, addr spotcontrol.GetAddressFunc, deviceId string) *Accesspoint
```

**Authentication Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `ConnectSpotifyToken(ctx, username, token)` | `error` | Authenticate with an OAuth access token |
| `ConnectStored(ctx, username, data)` | `error` | Authenticate with stored credentials |
| `ConnectBlob(ctx, username, blob)` | `error` | Authenticate with an encrypted discovery blob |
| `Connect(ctx, creds)` | `error` | Low-level: authenticate with `LoginCredentials` protobuf |

All `Connect*` methods perform the full flow: TCP connect → DH key exchange → challenge solve → Shannon cipher setup → authenticate → receive APWelcome. Retries up to 5 times with 500ms constant backoff (login errors are permanent).

**Packet I/O:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Send(ctx, pktType, payload)` | `error` | Send an encrypted packet |
| `Receive(types...)` | `<-chan Packet` | Register to receive packet types; starts recv loop on first call |

**State Access:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Username()` | `string` | Canonical username from APWelcome |
| `StoredCredentials()` | `[]byte` | Reusable auth credential bytes |
| `Welcome()` | `*pb.APWelcome` | Raw APWelcome protobuf |
| `Close()` | | Terminates connection and background goroutines |

**Background Processing:**
- **Recv loop**: Reads encrypted packets and dispatches to registered channels
- **Pong-ack ticker**: Sends pong-ack packets at regular intervals; triggers reconnection if no response
- **Reconnection**: Automatic reconnection using stored credentials from APWelcome

### `Packet`

```go
type Packet struct {
    Type    PacketType
    Payload []byte
}
```

### `PacketType`

Key packet type constants:

| Constant | Value | Description |
|----------|-------|-------------|
| `PacketTypePing` | `0x04` | Server ping |
| `PacketTypePong` | `0x49` | Pong response |
| `PacketTypeLogin` | `0xab` | Login request |
| `PacketTypeAPWelcome` | `0xac` | Authentication success |
| `PacketTypeAuthFailure` | `0xad` | Authentication failure |
| `PacketTypeMercuryReq` | `0xb2` | Mercury request/response |
| `PacketTypeMercurySub` | `0xb3` | Mercury subscribe |
| `PacketTypeMercuryUnsub` | `0xb4` | Mercury unsubscribe |
| `PacketTypeMercuryEvent` | `0xb5` | Mercury push event |
| `PacketTypeCountryCode` | `0x1b` | Country code |
| `PacketTypeProductInfo` | `0x50` | Product info |

### `AccesspointLoginError`

```go
type AccesspointLoginError struct {
    Message *pb.APLoginFailed
}
```

Returned when authentication fails. Treated as a permanent error (no retries).

---

## `dh` (Diffie-Hellman)

**Import**: `github.com/mcMineyC/spotcontrol/dh`

The `dh` package implements Diffie-Hellman key exchange using the well-known 768-bit MODP group used by Spotify's access point protocol.

### `DiffieHellman`

**Constructor:**

```go
func NewDiffieHellman() (*DiffieHellman, error)
```

Generates a new key pair with a random 95-byte private key.

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `PublicKeyBytes()` | `[]byte` | Local public key for sending in ClientHello |
| `Exchange(remoteKeyBytes)` | `[]byte` | Compute shared secret from the server's public key |
| `SharedSecretBytes()` | `[]byte` | Returns the shared secret (panics if `Exchange` not called) |

**Parameters:**
- Generator: `g = 2`
- Prime: 768-bit MODP prime (96 bytes)

---

## `login5`

**Import**: `github.com/mcMineyC/spotcontrol/login5`

The `login5` package handles authentication against Spotify's Login5 endpoint (`https://login5.spotify.com/v3/login`). It supports hashcash challenge solving and automatic token renewal.

### `Login5`

**Constructor:**

```go
func NewLogin5(log spotcontrol.Logger, client *http.Client, deviceId, clientToken string) *Login5
```

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Login(ctx, credentials)` | `error` | Authenticate with Login5 (auto-solves hashcash challenges) |
| `Username()` | `string` | Canonical username (panics if not authenticated) |
| `StoredCredential()` | `[]byte` | Stored credential bytes (panics if not authenticated) |
| `AccessToken()` | `GetLogin5TokenFunc` | Returns a function that auto-renews the access token |

**Supported credential types** (protobuf messages):
- `StoredCredential` — reusable credential from AP
- `Password` — plain password
- `FacebookAccessToken`
- `OneTimeToken`
- `ParentChildCredential`
- `AppleSignInCredential`
- `SamsungSignInCredential`
- `GoogleSignInCredential`

### `LoginError`

```go
type LoginError struct {
    Code pb.LoginError
}
```

Returned when the Login5 endpoint returns an error code.

### Hashcash Challenge Solving

The `solveHashcash` function (internal) solves Login5 hashcash challenges:

1. Compute SHA-1 of the login context
2. Seed a 16-byte suffix from bytes 12–20 of the hash
3. Concatenate the challenge prefix + suffix
4. Repeatedly SHA-1 hash, incrementing the suffix, until the result has the required number of trailing zero bits
5. Return the suffix and computation duration

---

## `apresolve`

**Import**: `github.com/mcMineyC/spotcontrol/apresolve`

The `apresolve` package fetches and caches Spotify service endpoint URLs from `https://apresolve.spotify.com/`. Results are cached for 1 hour.

### `ApResolver`

**Constructor:**

```go
func NewApResolver(log spotcontrol.Logger, client *http.Client) *ApResolver
```

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `GetAccesspoint(ctx)` | `(GetAddressFunc, error)` | Rotating accesspoint addresses |
| `GetSpclient(ctx)` | `(GetAddressFunc, error)` | Rotating spclient addresses |
| `GetDealer(ctx)` | `(GetAddressFunc, error)` | Rotating dealer addresses |
| `FetchAll(ctx)` | `error` | Pre-fetch all three endpoint types |

Each `Get*` method returns a `GetAddressFunc` that rotates through the fetched addresses. When the list is exhausted, new addresses are fetched automatically.

**Endpoint types resolved:**
- `accesspoint` — TCP AP servers (e.g. `ap-gue1.spotify.com:4070`)
- `dealer` — WebSocket dealer servers (e.g. `dealer.spotify.com`)
- `spclient` — HTTPS spclient servers (e.g. `spclient-wg.spotify.com:443`)

---

## `dealer`

**Import**: `github.com/mcMineyC/spotcontrol/dealer`

The `dealer` package manages a WebSocket connection to a Spotify dealer endpoint. It handles authentication, ping/pong keep-alive, automatic reconnection with exponential back-off, and message dispatching.

### `Dealer`

**Constructor:**

```go
func NewDealer(
    log spotcontrol.Logger,
    client *http.Client,
    dealerAddr spotcontrol.GetAddressFunc,
    accessToken spotcontrol.GetLogin5TokenFunc,
) *Dealer
```

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Connect(ctx)` | `error` | Open WebSocket connection (no-op if already connected) |
| `Close()` | | Terminate connection and close all receiver channels |
| `ConnectionId()` | `string` | Spotify-Connection-Id from the WebSocket handshake |
| `ReceiveMessage(uriPrefixes...)` | `<-chan Message` | Register for messages matching URI prefixes |
| `ReceiveRequest(uri)` | `<-chan Request` | Register for requests matching a URI (one receiver per URI) |

**Connection details:**
- URL: `wss://{addr}/?access_token={token}`
- Ping interval: 30 seconds
- Pong timeout: 30 + 10 seconds
- Reconnection: exponential back-off

### `Message`

```go
type Message struct {
    Uri     string
    Headers map[string]string
    Payload []byte  // Decompressed (gzip) and decoded (base64)
}
```

### `Request`

```go
type Request struct {
    MessageIdent string
    Payload      RequestPayload
}
```

**Method:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Reply(success)` | | Send success/failure response back to dealer |

### `RequestPayload`

Contains the full command details for connect-state playback control requests. Key fields include `MessageId`, `SentByDeviceId`, and a nested `Command` struct with `Endpoint`, `Context`, `PlayOrigin`, `Track`, `Options`, `PlayOptions`, etc.

### `RawMessage`

The JSON-framed message received over the WebSocket (internal parsing type):

```go
type RawMessage struct {
    Type         string            `json:"type"`      // "message", "request", "ping", "pong"
    Method       string            `json:"method"`
    Uri          string            `json:"uri"`
    Headers      map[string]string `json:"headers"`
    MessageIdent string            `json:"message_ident"`
    Key          string            `json:"key"`
    Payloads     []interface{}     `json:"payloads"`
    Payload      struct {
        Compressed []byte `json:"compressed"`
    } `json:"payload"`
}
```

---

## `spclient`

**Import**: `github.com/mcMineyC/spotcontrol/spclient`

The `spclient` package wraps HTTP requests to Spotify's private spclient infrastructure and the public Web API. It handles token injection, retries with back-off for 401/429 errors, and provides connect-state specific endpoints.

### `Spclient`

**Constructor:**

```go
func NewSpclient(
    ctx context.Context,
    log spotcontrol.Logger,
    client *http.Client,
    addr spotcontrol.GetAddressFunc,
    accessToken spotcontrol.GetLogin5TokenFunc,
    deviceId, clientToken string,
) (*Spclient, error)
```

**General Request Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Request(ctx, method, path, query, header, body)` | `(*http.Response, error)` | Spclient request (Login5 token, with retry) |
| `WebApiRequest(ctx, method, path, query, header, body)` | `(*http.Response, error)` | Web API request (OAuth2 token, with retry) |
| `SetWebApiTokenFunc(fn)` | | Set the OAuth2 token function for Web API requests |
| `GetAccessToken(ctx, force)` | `(string, error)` | Get a Login5 token |
| `DeviceId()` | `string` | The device ID |

**Retry Behavior:**
- **401 Unauthorized**: Force-refreshes token, retries up to 2 times
- **429 Too Many Requests**: Waits for `Retry-After` header (max 60s, default 5s), retries up to 5 total times
- **503 Service Unavailable**: Same retry behavior as 429

**Connect-State Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `PutConnectState(ctx, connId, req)` | `([]byte, error)` | PUT device state to connect-state backend |
| `PutConnectStateInactive(ctx, connId)` | `error` | Mark device as inactive |
| `ConnectPlayerCommand(ctx, connId, target, cmdReq)` | `error` | Send player command via connect-state |
| `ConnectTransfer(ctx, connId, from, to, req)` | `error` | Transfer playback between devices |
| `ConnectSetVolume(ctx, connId, targetDeviceId, volume)` | `error` | Set volume via connect-state |

**Metadata:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `GetTrackMetadata(ctx, trackHexId)` | `(*TrackMetadata, error)` | Fetch track metadata from private API |

### Command Types

The `spclient` package defines several JSON-serializable types used for building connect-state player commands:

- **`PlayerCommand`** — The main command payload (endpoint, context, play origin, options, etc.)
- **`PlayerCommandRequest`** — Wraps a `PlayerCommand` with connection_type and intent_id
- **`PreparePlayOptions`** — Playback preparation options (skip_to, shuffle, session_id, etc.)
- **`PlayOptions`** — Play behavior (reason, operation, trigger)
- **`CommandOptions`** — Command-level options (allow_seeking, override_restrictions)
- **`CommandLoggingParams`** — Timing and correlation IDs
- **`TransferRequest`** / **`TransferOptions`** — Transfer playback options
- **`ResumeOrigin`** — Resume origin info (feature_identifier)
- **`CommandSkipTo`** — Skip to a specific track

### `TrackMetadata`

```go
type TrackMetadata struct {
    Gid          string
    Name         string
    Album        *TrackMetadataAlbum
    Artist       []TrackMetadataArtist
    Number       int
    DiscNumber   int
    Duration     int64
    Popularity   int
    CanonicalUri string
    ExternalId   []TrackExternalId
    MediaType    string
}
```

**Convenience Methods:**

| Method | Returns | Description |
|--------|---------|-------------|
| `ArtistName()` | `string` | Primary artist name |
| `AlbumName()` | `string` | Album name |
| `LargeImageURL()` | `string` | 640px cover art URL |
| `DefaultImageURL()` | `string` | 300px cover art URL |
| `SmallImageURL()` | `string` | 64px cover art URL |
| `ImageURL(size)` | `string` | Cover art by size (`"SMALL"`, `"DEFAULT"`, `"LARGE"`) |

Image URLs use the format `https://i.scdn.co/image/{file_id}`.

### Utility Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `RandomHex(n)` | `string` | Generate `n` random hex bytes as a string (2n chars) |

---

## `mercury`

**Import**: `github.com/mcMineyC/spotcontrol/mercury`

The `mercury` package implements Mercury (Hermes) request/response and pub/sub messaging over the AP connection. It handles multi-part packet assembly, sequence numbering, and subscription management.

### `Client`

**Constructor:**

```go
func NewClient(log spotcontrol.Logger, accesspoint *ap.Accesspoint) *Client
```

Registers to receive `MercuryReq`, `MercurySub`, `MercuryUnsub`, and `MercuryEvent` packets from the AP. Starts a background receive loop.

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `Do(ctx, req)` | `(*Response, error)` | Send a request and wait for the response |
| `Subscribe(ctx, uri)` | `(*Subscription, error)` | Create a pub/sub subscription |
| `Unsubscribe(ctx, uri)` | `error` | Remove a subscription |
| `Close()` | | Stop the receive loop |

### `Request`

```go
type Request struct {
    Method      string    // e.g. "GET", "SUB", "UNSUB"
    Uri         string    // Mercury URI
    ContentType string
    Payload     [][]byte  // Additional payload parts
}
```

### `Response`

```go
type Response struct {
    HeaderData []byte
    Uri        string
    StatusCode int32
    Payload    [][]byte  // Payload parts (excluding the header)
}
```

### `Subscription`

```go
type Subscription struct {
    Uri string
    Ch  <-chan Response  // Delivers matching push events
}
```

### Wire Format

Mercury packets over the AP use the following binary format:

```
[2 bytes] sequence length (big-endian)
[N bytes] sequence number (big-endian uint64)
[1 byte]  flags
[2 bytes] number of parts (big-endian)
For each part:
  [2 bytes] part length (big-endian)
  [N bytes] part data
```

The first part is always a serialized `MercuryHeader` protobuf containing the URI, method, content type, and status code. Subsequent parts are the payload.

Subscription events are matched by exact URI or by wildcard prefix (URIs ending with `*`).

---

## Next Steps

- **[Controller Guide](controller-guide.md)** — Detailed usage patterns for the controller API
- **[Protocol Details](protocol-details.md)** — Wire-level protocol internals
- **[Architecture](architecture.md)** — System design and component relationships