# Architecture

This document describes the internal architecture of spotcontrol, how the components connect to each other, and the data flow through the system.

## System Overview

spotcontrol is organized as a layered stack of components, each responsible for a specific part of the Spotify protocol. The `Session` object wires them all together, and the `Controller` provides a high-level API on top.

```
┌─────────────────────────────────────────────────────────┐
│                     quick.Connect()                      │
│  (one-liner convenience: state + session + controller)   │
├─────────────────────────────────────────────────────────┤
│                      Controller                          │
│  (high-level: play/pause/skip, volume, devices, events)  │
│  Uses: Spclient, Dealer                                  │
├──────────┬──────────┬────────────┬──────────────────────┤
│          │          │            │                       │
│    AP    │  Login5  │  Spclient  │       Dealer          │
│   (TCP)  │ (HTTPS)  │  (HTTPS)   │    (WebSocket)        │
│          │          │            │                       │
├──────────┤          │            │                       │
│ Mercury  │          │            │                       │
│ (pub/sub)│          │            │                       │
└──────────┴──────────┴────────────┴──────────────────────┘
         ▲                ▲              ▲
         │                │              │
    ┌────┴────┐    ┌──────┴──────┐  ┌───┴────┐
    │   DH    │    │ AP Resolver │  │ Client │
    │KeyExch  │    │ (apresolve) │  │ Token  │
    └─────────┘    └─────────────┘  └────────┘
```

## Component Responsibilities

### Session (`session` package)

The `Session` is the central orchestrator. It creates and wires together all lower-level components during construction:

1. **Client Token Retrieval** — Fetches a client token from `clienttoken.spotify.com`
2. **AP Resolution** — Discovers endpoint addresses via `apresolve.spotify.com`
3. **AP Connection** — Connects to the access point over TCP, performs DH key exchange, and authenticates
4. **Login5 Authentication** — Authenticates with the Login5 HTTPS endpoint using stored credentials from the AP
5. **Spclient Initialization** — Creates the HTTP client for the spclient API
6. **Dealer Initialization** — Creates the WebSocket client for real-time push messages
7. **Mercury Initialization** — Creates the pub/sub messaging client over the AP connection

The session holds references to all components and provides accessor methods (`Accesspoint()`, `Spclient()`, `Dealer()`, `Mercury()`, etc.). It also manages the OAuth2 token lifecycle for Web API access.

**Key methods:**
- `NewSessionFromOptions(ctx, opts)` — Full connection and authentication flow
- `ExportState()` — Captures session state for persistence
- `NewController()` — Creates a pre-configured Controller
- `Close()` — Shuts down all connections

### Access Point (`ap` package)

The Access Point (AP) is the primary TCP connection to Spotify's backend. All low-level protocol communication happens over this encrypted connection.

**Connection flow:**

```
Client                          Access Point
  │                                  │
  │──── ClientHello (DH public) ────▶│
  │◀─── APResponse (DH public) ─────│
  │                                  │
  │  (Both sides compute shared secret)
  │  (Shannon cipher keys derived)   │
  │                                  │
  │──── ClientResponsePlaintext ────▶│
  │     (HMAC challenge solution)    │
  │                                  │
  │  ═══ Encrypted from here on ═══  │
  │                                  │
  │──── Login (credentials) ────────▶│
  │◀─── APWelcome / AuthFailure ────│
  │                                  │
  │◀──── Ping ──────────────────────│
  │───── Pong ─────────────────────▶│
  │◀──── PongAck ──────────────────│
  │                                  │
  │◀──── Mercury packets ──────────│
  │───── Mercury packets ──────────▶│
```

**Key characteristics:**
- **Shannon stream cipher** — All packets after key exchange are encrypted with the Shannon cipher, using separate send/receive keys derived from the DH shared secret
- **Packet framing** — Each packet has a 1-byte type, 2-byte payload length, and a 4-byte MAC
- **Ping/Pong keep-alive** — The AP sends periodic pings; the client responds with pongs. PongAck timeout triggers reconnection (120s interval)
- **Automatic reconnection** — On connection loss, the AP reconnects using exponential backoff and re-authenticates with the stored credentials from the original APWelcome
- **Packet dispatching** — Received packets are routed to registered channel receivers based on packet type

**Packet types handled:**

| Type | Code | Description |
|------|------|-------------|
| `Ping` | `0x04` | Server keep-alive ping |
| `Pong` | `0x49` | Client keep-alive response |
| `PongAck` | `0x4a` | Server acknowledgment of pong |
| `Login` | `0xab` | Authentication request |
| `APWelcome` | `0xac` | Successful authentication response |
| `AuthFailure` | `0xad` | Failed authentication response |
| `MercuryReq` | `0xb2` | Mercury request/response |
| `MercurySub` | `0xb3` | Mercury subscribe |
| `MercuryUnsub` | `0xb4` | Mercury unsubscribe |
| `MercuryEvent` | `0xb5` | Mercury push event |
| `CountryCode` | `0x1b` | User's country code |
| `ProductInfo` | `0x50` | Account product information |

### Diffie-Hellman (`dh` package)

Implements the Diffie-Hellman key exchange using Spotify's well-known 768-bit MODP group. This is used during the AP handshake to establish a shared secret for deriving Shannon cipher keys.

- Generates a random 95-byte private key using `crypto/rand`
- Computes the public key as `g^private mod p`
- The `Exchange()` method computes the shared secret from the remote party's public key

### Login5 (`login5` package)

Login5 is Spotify's modern token-based authentication system. After the AP connection is established, Login5 is used to obtain bearer tokens for the spclient and dealer APIs.

**Authentication flow:**

```
Client                         login5.spotify.com
  │                                    │
  │──── LoginRequest ─────────────────▶│
  │     (credentials + client info)    │
  │                                    │
  │◀─── LoginResponse ────────────────│
  │     (challenges: hashcash)         │
  │                                    │
  │──── LoginRequest ─────────────────▶│
  │     (challenge solutions)          │
  │                                    │
  │◀─── LoginResponse ────────────────│
  │     (LoginOk: access_token,        │
  │      stored_credential, username)  │
```

**Key characteristics:**
- **Protobuf over HTTPS** — Requests and responses are serialized as protobuf, sent via `POST` to `https://login5.spotify.com/v3/login`
- **Hashcash challenges** — The server may respond with a hashcash challenge requiring the client to find a suffix that produces a SHA-1 hash with a specified number of trailing zero bits
- **Automatic token renewal** — The `AccessToken()` function returns a `GetLogin5TokenFunc` that transparently renews the token when it expires by re-authenticating with stored credentials
- **Multiple credential types** — Supports `StoredCredential`, `Password`, `FacebookAccessToken`, `OneTimeToken`, `ParentChildCredential`, `AppleSignInCredential`, `SamsungSignInCredential`, and `GoogleSignInCredential`

### AP Resolver (`apresolve` package)

Discovers and caches the network addresses of Spotify's service endpoints. All three endpoint types are fetched from `https://apresolve.spotify.com/`:

| Endpoint Type | Description | Example |
|---------------|-------------|---------|
| `accesspoint` | AP TCP connection addresses | `ap-gae2.spotify.com:4070` |
| `dealer` | Dealer WebSocket addresses | `dealer.spotify.com:443` |
| `spclient` | Spclient HTTPS addresses | `spclient-wg.spotify.com:443` |

**Key characteristics:**
- **1-hour TTL cache** — Resolved addresses are cached and automatically refreshed when expired
- **Rotating addresses** — `GetAddressFunc` iterates through available addresses, automatically fetching new ones when exhausted
- **Batch fetching** — `FetchAll()` fetches all three types in a single request

### Dealer (`dealer` package)

The Dealer manages a WebSocket connection to Spotify's dealer endpoint for real-time push notifications. It is the primary mechanism for receiving Connect State cluster updates.

**Key characteristics:**
- **JSON-framed messages** — Messages are JSON objects with `type`, `uri`, `headers`, and `payloads` fields
- **Message types**: `message` (push notifications), `request` (requires reply), `ping`/`pong` (keep-alive)
- **URI-based routing** — Message receivers register URI prefixes; matching messages are dispatched to the appropriate channel
- **Request/reply** — Some dealer messages are requests that require a `success`/`failure` reply
- **Gzip decompression** — Payloads with `Transfer-Encoding: gzip` are automatically decompressed
- **Base64 decoding** — String payloads are automatically base64-decoded
- **Ping/pong keep-alive** — 30-second ping interval with 10-second timeout
- **Automatic reconnection** — Reconnects with exponential backoff on connection loss
- **Spotify-Connection-Id** — The WebSocket upgrade response or a subsequent pusher message contains the `Spotify-Connection-Id` header, required for connect-state API calls

**Important dealer message URIs:**

| URI Prefix | Description |
|------------|-------------|
| `hm://pusher/v1/connections/` | Delivers the Spotify-Connection-Id |
| `hm://connect-state/v1/cluster` | Connect State cluster updates (device list, playback state) |

### Spclient (`spclient` package)

The Spclient is an HTTP client for Spotify's spclient API. It handles two categories of requests:

1. **Spclient endpoints** — Connect State management, metadata API, and other private APIs on the spclient base URL
2. **Web API proxy** — Proxied requests to `api.spotify.com` for the public Spotify Web API

**Key characteristics:**
- **Automatic token injection** — Bearer token (Login5 or OAuth2) and client token are automatically added to every request
- **Retry logic** — Handles 401 (token refresh and retry), 429 (respects `Retry-After`), and 502/503 (exponential backoff)
- **Dual token support** — Spclient endpoints use the Login5 token; Web API requests use the OAuth2 token if available
- **Connect State API** — `PutConnectState()` registers/updates the device; response contains the initial `Cluster` protobuf

**Connect State endpoints:**

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/connect-state/v1/devices/{deviceId}` | Register/update device state |
| `PUT` | `/connect-state/v1/devices/{deviceId}/inactive` | Mark device as inactive |
| `POST` | `/connect-state/v1/player/command/from/{from}/to/{to}` | Send player command (gzip JSON) |
| `POST` | `/connect-state/v1/connect/transfer/from/{from}/to/{to}` | Transfer playback |
| `PUT` | `/connect-state/v1/connect/volume/from/{from}/to/{to}` | Set volume (protobuf body) |
| `GET` | `/metadata/4/track/{hexId}?market=from_token` | Fetch track metadata (JSON) |

### Mercury (`mercury` package)

Mercury (also known as Hermes) provides pub/sub messaging over the AP TCP connection. It's used for legacy protocol support and certain real-time features.

**Key characteristics:**
- **Binary framing** — Messages are framed with sequence numbers, flags, part counts, and variable-length parts
- **Protobuf headers** — Each Mercury message has a `MercuryHeader` protobuf containing the URI, method, and content type
- **Request/response** — `Do()` sends a request and waits for the response on a sequence-matched channel
- **Subscriptions** — `Subscribe()` creates a subscription; matching push events (`MercuryEvent` packets) are delivered to the subscription channel
- **Wildcard matching** — Subscription URIs ending with `*` match any URI with that prefix

### Controller (`controller` package)

The Controller is the high-level public API that sits on top of the Spclient and Dealer. It provides:

- **Device listing** — From the cached cluster state or the Web API
- **Playback control** — Play, pause, next, previous, seek, shuffle, repeat
- **Track loading** — Load tracks by URI, play playlists, play individual tracks via connect-state
- **Volume control** — Debounced volume changes via the connect-state volume endpoint
- **Playback transfer** — Transfer between devices via the connect-state transfer endpoint
- **Queue management** — Add tracks to the queue
- **Player state** — From the cached cluster or the Web API
- **Event subscriptions** — Real-time channels for device list changes, playback changes, and track metadata
- **Track metadata** — Rich metadata (title, artist, album, cover art) fetched from the private metadata API

**Command routing:**

By default, commands are routed through the connect-state player command endpoint:

```
POST /connect-state/v1/player/command/from/{ourDevice}/to/{targetDevice}
```

The body is a gzip-compressed JSON `PlayerCommandRequest`. If the connect-state path fails, the controller falls back to the Spotify Web API proxy (`/v1/me/player/*`) through the spclient.

Volume uses a dedicated connect-state endpoint with protobuf body:
```
PUT /connect-state/v1/connect/volume/from/{ourDevice}/to/{targetDevice}
```

Transfer uses the connect-state transfer endpoint:
```
POST /connect-state/v1/connect/transfer/from/{fromDevice}/to/{toDevice}
```

### Quick (`quick` package)

The `quick` package provides the `Connect()` convenience function that wires together state persistence, authentication, session creation, and controller startup in a single call. It is the recommended entry point for most use cases.

## Data Flow

### Startup Sequence

```
1. quick.Connect() / NewSessionFromOptions()
   │
   ├── Retrieve client token (HTTPS → clienttoken.spotify.com)
   │
   ├── Resolve endpoints (HTTPS → apresolve.spotify.com)
   │   └── Returns: AP, spclient, dealer addresses
   │
   ├── Connect AP (TCP → accesspoint)
   │   ├── DH key exchange
   │   ├── Shannon cipher established
   │   └── Login with credentials → APWelcome (stored credentials)
   │
   ├── Login5 (HTTPS → login5.spotify.com)
   │   ├── Send stored credentials
   │   ├── Solve hashcash challenge
   │   └── Receive access token
   │
   ├── Initialize Spclient (HTTPS wrapper → spclient address)
   │
   ├── Initialize Dealer (WebSocket → dealer address)
   │
   └── Initialize Mercury (over AP connection)

2. Controller.Start()
   │
   ├── Dealer.Connect() (WebSocket handshake)
   │
   ├── Subscribe to dealer messages:
   │   ├── hm://pusher/v1/connections/ → Connection ID
   │   └── hm://connect-state/v1/cluster → Cluster updates
   │
   ├── Receive Connection ID from dealer
   │
   └── RegisterDevice() → PutConnectState(NEW_DEVICE)
       └── Response: initial Cluster (devices, player state)
```

### Playback Control Flow

```
Controller.Play(ctx, deviceId)
   │
   ├── Build PlayerCommand { endpoint: "resume", ... }
   │
   ├── Try connect-state path:
   │   └── POST /connect-state/v1/player/command/from/{us}/to/{target}
   │       (gzip-compressed JSON PlayerCommandRequest)
   │
   └── On failure, fall back to Web API:
       └── PUT /v1/me/player/play?device_id=...
           (via spclient proxy or api.spotify.com)
```

### Real-time Event Flow

```
Spotify Backend
   │
   │ (WebSocket push)
   ▼
Dealer
   │ ClusterUpdate message on hm://connect-state/v1/cluster
   │
   ▼
Controller.handleClusterUpdate()
   │
   ├── Update cached cluster
   │
   ├── Diff device list → emit DeviceListEvent
   │
   ├── Extract player state → emit PlaybackEvent
   │
   └── Detect track change → async fetch metadata
       │                      from GET /metadata/4/track/{hexId}
       └── emit MetadataEvent
```

## Thread Safety

All components are safe for concurrent use:

- **AP** — Send/receive operations use independent locks; the connection mutex is held for writing only during reconnection
- **Login5** — Token storage is protected by `sync.RWMutex`; the `AccessToken()` function can be called from multiple goroutines
- **Dealer** — Connection access is protected by `sync.RWMutex`; message/request receiver lists are independently locked
- **Spclient** — The underlying `http.Client` is inherently concurrent-safe
- **Mercury** — Pending requests and subscriptions are protected by separate mutexes
- **Controller** — Cluster state, connection ID, volume debouncing, event subscribers, and metadata cache all have independent locks

## Reconnection Strategy

Both the AP and Dealer implement automatic reconnection:

- **AP** — On connection loss, the recv loop detects the read error, closes the connection, and retries with exponential backoff using `github.com/cenkalti/backoff/v4`. Reconnection re-authenticates using the stored credentials from the original `APWelcome`.
- **Dealer** — Similarly uses exponential backoff for reconnection. After reconnection, the recv loop restarts. Receiver channels are NOT closed during reconnection (only on explicit `Close()`), so consumers don't need special reconnection handling.
- **PongAck timeout** — The AP monitors the time since the last PongAck. If 120 seconds pass without one, the connection is forcibly closed, triggering reconnection.
- **Ping timeout** — The Dealer monitors pong responses. If no pong arrives within 40 seconds (30s interval + 10s timeout), the connection is forcibly closed.

## Dependency Graph

```
quick ──▶ session ──▶ ap ──────▶ dh
                  │          └──▶ shannon (external)
                  ├──▶ login5
                  ├──▶ spclient
                  ├──▶ dealer ──▶ websocket (external)
                  ├──▶ mercury ──▶ ap
                  └──▶ apresolve

controller ──▶ spclient
           ├──▶ dealer
           └──▶ proto (protobuf types)

spotcontrol (root) ──▶ proto (device types, AppState, Logger)
```

## External Dependencies

| Dependency | Version | Purpose |
|-----------|---------|---------|
| `github.com/cenkalti/backoff/v4` | v4.3.0 | Exponential backoff for reconnection |
| `github.com/coder/websocket` | v1.8.14 | WebSocket client for the Dealer |
| `github.com/devgianlu/shannon` | latest | Shannon stream cipher implementation |
| `golang.org/x/crypto` | v0.25.0 | PBKDF2 for blob decryption |
| `golang.org/x/oauth2` | v0.21.0 | OAuth2 PKCE authentication flow |
| `google.golang.org/protobuf` | v1.34.2 | Protocol buffer serialization |

## Next Steps

- **[Package Reference](package-reference.md)** — Detailed API for each package
- **[Protocol Details](protocol-details.md)** — Low-level protocol wire formats
- **[Controller Guide](controller-guide.md)** — How to use the high-level API