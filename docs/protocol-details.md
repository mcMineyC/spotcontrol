# Protocol Details

This document describes the low-level Spotify protocols implemented by spotcontrol. It covers the wire formats, message flows, and cryptographic details for each protocol layer.

## Table of Contents

- [Protocol Stack Overview](#protocol-stack-overview)
- [Access Point (AP) Protocol](#access-point-ap-protocol)
  - [Connection Establishment](#connection-establishment)
  - [Diffie-Hellman Key Exchange](#diffie-hellman-key-exchange)
  - [Challenge Response](#challenge-response)
  - [Shannon Stream Cipher](#shannon-stream-cipher)
  - [Packet Format](#packet-format)
  - [Packet Types](#packet-types)
  - [Authentication](#authentication)
  - [Ping/Pong Keep-Alive](#pingpong-keep-alive)
  - [Reconnection](#reconnection)
- [Client Token Protocol](#client-token-protocol)
- [Login5 Protocol](#login5-protocol)
  - [Request/Response Flow](#requestresponse-flow)
  - [Hashcash Challenge](#hashcash-challenge)
  - [Token Lifecycle](#token-lifecycle)
- [Dealer WebSocket Protocol](#dealer-websocket-protocol)
  - [Connection Setup](#dealer-connection-setup)
  - [Message Format](#dealer-message-format)
  - [Message Types](#dealer-message-types)
  - [Ping/Pong](#dealer-pingpong)
  - [Request/Reply](#dealer-requestreply)
  - [Transfer Encoding](#transfer-encoding)
  - [Connection ID](#connection-id)
- [Mercury (Hermes) Protocol](#mercury-hermes-protocol)
  - [Wire Format](#mercury-wire-format)
  - [Request/Response](#mercury-requestresponse)
  - [Pub/Sub](#mercury-pubsub)
  - [Subscription Matching](#subscription-matching)
- [Spclient HTTP API](#spclient-http-api)
  - [Authentication Headers](#spclient-authentication-headers)
  - [Connect-State Endpoints](#connect-state-endpoints)
  - [Player Command Format](#player-command-format)
  - [Volume Signaling](#volume-signaling)
  - [Transfer Endpoint](#transfer-endpoint)
  - [Private Metadata API](#private-metadata-api)
  - [Web API Proxy](#web-api-proxy)
  - [Retry & Rate Limiting](#retry--rate-limiting)
- [AP Resolver](#ap-resolver)
- [Protobuf Messages](#protobuf-messages)
- [Security Considerations](#security-considerations)

---

## Protocol Stack Overview

spotcontrol implements five protocol layers, each building on the layers below:

```
┌──────────────────────────────────────────────────┐
│                  Controller API                   │
│          (playback control, events, metadata)     │
├──────────────┬───────────────┬────────────────────┤
│  Spclient    │    Dealer     │     Mercury        │
│  (HTTPS)     │  (WebSocket)  │   (over AP TCP)    │
├──────────────┴───────────────┴────────────────────┤
│              Login5 (HTTPS, protobuf)              │
├───────────────────────────────────────────────────┤
│         Access Point (TCP, Shannon cipher)         │
├───────────────────────────────────────────────────┤
│       AP Resolver (HTTPS to apresolve.spotify.com) │
└───────────────────────────────────────────────────┘
```

**Data flow summary:**

1. **AP Resolver** discovers endpoint addresses for AP, spclient, and dealer
2. **Client Token** is obtained from `clienttoken.spotify.com` (protobuf over HTTPS)
3. **AP** establishes an encrypted TCP connection using DH key exchange + Shannon cipher
4. **Login5** authenticates over HTTPS using stored credentials from AP, producing a bearer token
5. **Spclient** uses the bearer token for HTTP API requests (connect-state, metadata, Web API proxy)
6. **Dealer** connects via WebSocket with the bearer token for real-time push messaging
7. **Mercury** uses the AP TCP connection for pub/sub messaging

---

## Access Point (AP) Protocol

The AP protocol is a custom binary protocol over TCP. It provides the foundation for Spotify's backend communication.

### Connection Establishment

The connection flow has four phases:

```
Client                                      Server (AP)
  │                                            │
  │──── TCP Connect ──────────────────────────>│
  │                                            │
  │──── ClientHello (plaintext, DH pubkey) ──>│
  │<─── APResponseMessage (DH pubkey + sig) ──│
  │                                            │
  │──── ClientResponsePlaintext (HMAC) ──────>│
  │     [Shannon cipher active from here]      │
  │                                            │
  │──── Login (encrypted credentials) ───────>│
  │<─── APWelcome / AuthFailure (encrypted) ──│
  │                                            │
  │<──> Encrypted packet exchange ────────────>│
```

### Diffie-Hellman Key Exchange

spotcontrol uses the 768-bit MODP group for Diffie-Hellman key exchange:

- **Generator (g):** 2
- **Prime (p):** Well-known 96-byte prime (same as RFC 2409 Group 1)
- **Private key:** 95 random bytes from `crypto/rand`
- **Public key:** `g^private mod p`

The exchange proceeds as follows:

1. **Client generates a key pair:**
   - Private key: 95 random bytes
   - Public key: `g^private mod p` (sent in `ClientHello.LoginCryptoHello.DiffieHellman.Gc`)

2. **Server responds with its public key:**
   - Server's DH public key in `APResponseMessage.Challenge.LoginCryptoChallenge.DiffieHellman.Gs`
   - RSA signature of the server's public key in `GsSignature`

3. **Client verifies the server signature:**
   - SHA-1 hash of the server's public key bytes
   - Verified against the well-known Spotify RSA public key (2048-bit, e=65537)
   - If verification fails, the connection is aborted

4. **Shared secret computation:**
   - `shared_secret = server_pubkey^client_private mod p`

### Challenge Response

After the DH exchange, both sides derive encryption keys from the shared secret:

1. **Key derivation** (5 rounds of HMAC-SHA1):
   ```
   mac_key = shared_secret
   mac_data = []  (accumulated over 5 rounds)

   for i = 1 to 5:
       mac_data += HMAC-SHA1(mac_key, exchange_data || byte(i))
   ```
   Where `exchange_data` is the concatenation of all bytes sent and received during the key exchange (accumulated by `connAccumulator`).

2. **Challenge HMAC:**
   ```
   challenge_hmac = HMAC-SHA1(mac_data[0:20], exchange_data)
   ```

3. **Client sends `ClientResponsePlaintext`** containing the HMAC

4. **Shannon cipher keys are derived:**
   - Send key: `mac_data[20:52]` (32 bytes)
   - Receive key: `mac_data[52:84]` (32 bytes)

From this point, all communication is encrypted with the Shannon stream cipher.

### Shannon Stream Cipher

The Shannon cipher provides symmetric encryption for AP packets. Each direction (send/receive) has its own cipher instance and nonce counter.

**Properties:**
- 32-byte key per direction
- 4-byte nonce (uint32, incremented per packet)
- 4-byte MAC appended to each packet
- Independent send/receive locks for concurrent safety

**Encryption (send):**
```
cipher.NonceU32(send_nonce)
send_nonce++
cipher.Encrypt(header + payload)  // in-place
mac = cipher.Finish(4)            // 4-byte MAC
write(encrypted_data || mac)
```

**Decryption (receive):**
```
cipher.NonceU32(recv_nonce)
recv_nonce++
read(encrypted_header)            // 3 bytes
cipher.Decrypt(encrypted_header)  // in-place → [type, length_hi, length_lo]
read(encrypted_payload)           // length bytes
cipher.Decrypt(encrypted_payload) // in-place
read(mac)                         // 4 bytes
cipher.CheckMac(mac)              // verify
```

### Packet Format

Each AP packet consists of:

```
┌─────────┬──────────────┬─────────────────┬─────────┐
│ Type    │ Payload Len  │ Payload         │ MAC     │
│ (1 byte)│ (2 bytes BE) │ (variable)      │(4 bytes)│
└─────────┴──────────────┴─────────────────┴─────────┘
```

- **Type:** 1 byte — identifies the packet type
- **Payload Length:** 2 bytes, big-endian unsigned — length of the payload (max 65535)
- **Payload:** Variable-length data
- **MAC:** 4-byte Shannon MAC covering the encrypted type + length + payload

The entire type + length + payload is encrypted in-place before transmission.

### Packet Types

| Type | Hex | Name | Description |
|------|-----|------|-------------|
| `0x02` | `SecretBlock` | Secret block data |
| `0x04` | `Ping` | Server ping |
| `0x08` | `StreamChunk` | Audio stream chunk |
| `0x09` | `StreamChunkRes` | Audio stream chunk response |
| `0x0a` | `ChannelError` | Channel error |
| `0x0b` | `ChannelAbort` | Channel abort |
| `0x0c` | `RequestKey` | Request AES key |
| `0x0d` | `AesKey` | AES key response |
| `0x0e` | `AesKeyError` | AES key error |
| `0x19` | `Image` | Image data |
| `0x1b` | `CountryCode` | User's country code |
| `0x49` | `Pong` | Pong response |
| `0x4a` | `PongAck` | Pong acknowledgment |
| `0x4b` | `Pause` | Pause notification |
| `0x50` | `ProductInfo` | Product/subscription info |
| `0x69` | `LegacyWelcome` | Legacy welcome (unused) |
| `0x74` | `PreferredLocale` | Preferred locale |
| `0x76` | `LicenseVersion` | License version |
| `0xab` | `Login` | Login credentials |
| `0xac` | `APWelcome` | Authentication success |
| `0xad` | `AuthFailure` | Authentication failure |
| `0xb2` | `MercuryReq` | Mercury request/response |
| `0xb3` | `MercurySub` | Mercury subscribe |
| `0xb4` | `MercuryUnsub` | Mercury unsubscribe |
| `0xb5` | `MercuryEvent` | Mercury push event |

### Authentication

After the Shannon cipher is established, the client sends a `Login` packet containing a `ClientResponseEncrypted` protobuf:

```protobuf
message ClientResponseEncrypted {
    LoginCredentials login_credentials = 10;
    // ... other fields
}

message LoginCredentials {
    optional string username = 10;
    AuthenticationType typ = 20;
    optional bytes auth_data = 30;
}
```

**Authentication types used by spotcontrol:**

| Type | Enum Value | Source |
|------|------------|--------|
| `AUTHENTICATION_STORED_SPOTIFY_CREDENTIALS` | 0 | Stored credentials from previous APWelcome |
| `AUTHENTICATION_SPOTIFY_TOKEN` | 5 | OAuth access token |

**Blob authentication** (for zeroconf discovery) decrypts the blob before sending:
1. Compute `base_key = SHA-1(device_id)`
2. Derive AES key via PBKDF2: `key = PBKDF2-HMAC-SHA1(base_key, username, 256 iterations, 20 bytes)`
3. Further derive: `aes_key = SHA-1(key)` padded to 24 bytes (zero-filled) for AES-192
4. Decrypt the blob with AES-192-ECB
5. Parse the decrypted blob to extract the auth type and auth data

**Successful authentication returns `APWelcome`:**

```protobuf
message APWelcome {
    string canonical_username = 10;
    AccountType account_type_logged_in = 20;
    // Reusable credentials for subsequent sessions:
    AuthenticationType reusable_auth_credentials_type = 30;
    bytes reusable_auth_credentials = 40;
    // ... other fields
}
```

The `reusable_auth_credentials` can be persisted and used as `StoredCredentials` for future sessions, avoiding the need to re-enter the password.

### Ping/Pong Keep-Alive

The AP connection uses a ping/pong mechanism for keep-alive:

1. **Server sends `Ping` (0x04)** periodically
2. **Client responds with `Pong` (0x49)** echoing the ping payload
3. **Client sends `PongAck` (0x4a)** at regular intervals
4. If no pong-ack response is received within the timeout, the client triggers reconnection

### Reconnection

When the AP connection drops (network error, pong timeout, etc.):

1. The receive loop detects the error
2. The `reconnect()` method is called, which:
   - Uses the stored credentials from `APWelcome` to re-authenticate
   - Performs the full connect flow (DH exchange → challenge → authenticate)
   - Restarts the receive loop
3. All registered packet receivers continue receiving on the same channels

Reconnection requires a valid `APWelcome` — if none exists, the error is permanent.

---

## Client Token Protocol

The client token is obtained from `https://clienttoken.spotify.com/v1/clienttoken` using protobuf-over-HTTPS:

**Request:**
```
POST /v1/clienttoken
Content-Type: application/x-protobuf
Accept: application/x-protobuf

Body: ClientTokenRequest protobuf {
    request_type: REQUEST_CLIENT_DATA_REQUEST
    client_data: {
        client_version: "<spotify-like-version>"
        client_id: "65b708073fc0480ea92a077233ca87bd"
        connectivity_sdk_data: {
            platform_specific_data: <OS-specific protobuf>
        }
    }
}
```

**Response:**
```
ClientTokenResponse {
    response_type: RESPONSE_GRANTED_TOKEN_RESPONSE
    granted_token: {
        token: "<client_token_string>"
        expires_after_seconds: <int>
        // ...
    }
}
```

The response may also contain a challenge (hashcash or JavaScript evaluation). spotcontrol does not currently support client token challenges — an error is returned if one is received.

**Platform-specific data** varies by OS:
- **macOS:** `NativeDesktopMacOSData`
- **Windows:** `NativeDesktopWindowsData`
- **Linux/FreeBSD:** `NativeDesktopLinuxData`
- **Android:** `NativeAndroidData`
- **iOS:** `NativeIOSData`

---

## Login5 Protocol

Login5 is a protobuf-over-HTTPS protocol for obtaining bearer tokens from `https://login5.spotify.com/v3/login`.

### Request/Response Flow

```
Client                              login5.spotify.com
  │                                       │
  │── POST /v3/login ────────────────────>│
  │   (LoginRequest with credentials)     │
  │                                       │
  │<── LoginResponse ─────────────────────│
  │   (may contain challenges)            │
  │                                       │
  │── POST /v3/login ────────────────────>│  (if challenges)
  │   (LoginRequest with solutions)       │
  │                                       │
  │<── LoginResponse ─────────────────────│
  │   (LoginOk with access token)         │
```

**Request headers:**
```
POST /v3/login
Content-Type: application/x-protobuf
Accept: application/x-protobuf
User-Agent: spotcontrol/<version> Go/<go-version>
Client-Token: <client_token>
```

**LoginRequest protobuf:**
```protobuf
message LoginRequest {
    ClientInfo client_info = 1;           // client_id + device_id
    LoginMethod login_method = 2..N;      // one of: StoredCredential, Password, etc.
    bytes login_context = 3;              // from previous challenge response
    ChallengeSolutions challenge_solutions = 4;  // solved challenges
}
```

**LoginResponse can be one of:**

1. **`LoginOk`** — successful authentication:
   ```protobuf
   message LoginOk {
       string username = 1;
       string access_token = 2;
       bytes stored_credential = 3;
       int32 access_token_expires_in = 4;  // seconds
   }
   ```

2. **`Challenges`** — server requires challenge solving:
   ```protobuf
   message Challenges {
       repeated Challenge challenges = 1;
   }
   message Challenge {
       oneof challenge {
           HashcashChallenge hashcash = 1;
           CodeChallenge code = 2;
       }
   }
   ```

3. **`LoginError`** — authentication failed (e.g. `INVALID_CREDENTIALS`)

### Hashcash Challenge

When the server responds with a hashcash challenge, the client must find a suffix that produces a SHA-1 hash with a specified number of trailing zero bits:

**Algorithm:**
```
input:
    login_context     // bytes from the LoginResponse
    challenge.prefix  // bytes from the HashcashChallenge
    challenge.length  // required trailing zero bits

login_context_sha1 = SHA-1(login_context)

suffix = [16 bytes]
suffix[0:8] = login_context_sha1[12:20]    // seed from context hash
suffix[8:16] = [0, 0, 0, 0, 0, 0, 0, 0]   // counter portion

loop:
    hash = SHA-1(challenge.prefix || suffix)
    if trailing_zero_bits(hash) >= challenge.length:
        return HashcashSolution { suffix, duration }
    increment(suffix[0:8])
    increment(suffix[8:16])
```

The `trailing_zero_bits` check counts zero bits from the **least significant bit** of the **last byte** of the hash, working backwards.

**Solution:**
```protobuf
message HashcashSolution {
    bytes suffix = 1;          // the 16-byte suffix that solves the challenge
    Duration duration = 2;     // time taken to solve
}
```

### Token Lifecycle

The Login5 access token has a short lifetime (typically 1 hour, specified by `access_token_expires_in`).

**Automatic renewal:**

The `AccessToken()` method returns a `GetLogin5TokenFunc` that:

1. Checks if the cached token has expired
2. If expired (or `force=true`), re-authenticates using stored credentials:
   ```
   Login(ctx, &StoredCredential{Username: ..., Data: ...})
   ```
3. Returns the new access token

This is transparent to callers — the spclient and dealer automatically get fresh tokens.

---

## Dealer WebSocket Protocol

The dealer provides real-time push messaging for connect-state updates, playback commands, and other notifications.

### Dealer Connection Setup

```
wss://{dealer_address}/?access_token={login5_token}

Headers:
    User-Agent: spotcontrol/<version> Go/<go-version>

Response Headers:
    Spotify-Connection-Id: <connection_id>  (used for PutConnectState)
```

The `Spotify-Connection-Id` from the WebSocket upgrade response is captured and used for connect-state API requests. If not present in the upgrade response, it arrives as the first dealer message (URI prefix `hm://pusher/v1/connections/`).

### Dealer Message Format

All dealer messages are JSON-framed over WebSocket text frames.

**Incoming message structure:**
```json
{
    "type": "message" | "request" | "ping" | "pong",
    "method": "string",
    "uri": "hm://...",
    "headers": {
        "Transfer-Encoding": "gzip",
        "Spotify-Connection-Id": "..."
    },
    "message_ident": "string",
    "key": "string",
    "payloads": ["base64-encoded-data"],
    "payload": {
        "compressed": <bytes>
    }
}
```

### Dealer Message Types

#### `"message"` — Push Notification

Server pushes data to the client. The `uri` field determines the message topic:

| URI Prefix | Description | Payload |
|------------|-------------|---------|
| `hm://pusher/v1/connections/` | Connection ID notification | None (ID in headers) |
| `hm://connect-state/v1/cluster` | Cluster state update | Protobuf `ClusterUpdate` (gzip + base64) |

**Payload handling:**
1. If `payloads` array has entries, decode the first entry from base64
2. Check `headers["Transfer-Encoding"]`:
   - `"gzip"` → decompress with gzip
3. The resulting bytes are the message payload (often a protobuf)

#### `"request"` — Command Request

The server sends a command that expects a reply (e.g. playback control from another device):

```json
{
    "type": "request",
    "message_ident": "hm://connect-state/v1/player/command",
    "key": "<unique_key>",
    "headers": {"Transfer-Encoding": "gzip"},
    "payload": {
        "compressed": <gzip bytes>
    }
}
```

The compressed payload contains a JSON `RequestPayload` with command details (endpoint, context, play origin, options).

#### `"ping"` / `"pong"` — Keep-Alive

The client sends periodic pings; the server responds with pongs:

```json
{"type": "ping"}
```

```json
{"type": "pong"}
```

### Dealer Ping/Pong

- **Ping interval:** 30 seconds
- **Pong timeout:** 30 + 10 = 40 seconds
- If no pong is received within the timeout:
  1. The WebSocket connection is closed with `StatusServiceRestart`
  2. The receive loop detects the closure
  3. Reconnection with exponential back-off begins

### Dealer Request/Reply

When a `"request"` message arrives:

1. The payload is decompressed and parsed
2. The request is dispatched to a registered receiver channel
3. The receiver processes the request and calls `Request.Reply(success)`
4. The dealer sends a reply back to the server:

```json
{
    "type": "reply",
    "key": "<matching_key>",
    "payload": {
        "success": true
    }
}
```

### Transfer Encoding

Dealer payloads may use gzip compression, indicated by the `Transfer-Encoding: gzip` header:

```
Headers: {"Transfer-Encoding": "gzip"}
Payload: gzip-compressed bytes

→ After decompression: raw protobuf or JSON bytes
```

The `Transfer-Encoding` header is removed after processing.

### Connection ID

The `Spotify-Connection-Id` is critical for connect-state operations. It is obtained in two possible ways:

1. **WebSocket upgrade response header:** The `Spotify-Connection-Id` header in the HTTP upgrade response
2. **Dealer message:** A message with URI prefix `hm://pusher/v1/connections/` containing the ID in the `Spotify-Connection-Id` message header

This ID is included in all `PutConnectState` requests as the `X-Spotify-Connection-Id` header.

---

## Mercury (Hermes) Protocol

Mercury provides request/response and pub/sub messaging over the AP TCP connection. It is used for various internal Spotify operations.

### Mercury Wire Format

Mercury packets are carried inside AP packets of types `0xb2` (MercuryReq), `0xb3` (MercurySub), `0xb4` (MercuryUnsub), and `0xb5` (MercuryEvent).

**Packet payload structure:**

```
┌────────────┬─────────────┬───────┬────────────┬─────────────────────┐
│ Seq Length │ Sequence    │ Flags │ Part Count │ Parts...            │
│ (2B BE)    │ (N bytes)   │ (1B)  │ (2B BE)    │                     │
└────────────┴─────────────┴───────┴────────────┴─────────────────────┘

Each Part:
┌─────────────┬──────────┐
│ Part Length  │ Part Data│
│ (2B BE)     │ (N bytes)│
└─────────────┴──────────┘
```

- **Seq Length:** 2 bytes big-endian — length of the sequence number (typically 8)
- **Sequence:** Big-endian uint64 sequence number for correlating requests with responses
- **Flags:** 1 byte (typically 1)
- **Part Count:** 2 bytes big-endian — number of parts that follow
- **Parts:** Each part has a 2-byte big-endian length prefix followed by the part data

**The first part is always a `MercuryHeader` protobuf:**

```protobuf
message MercuryHeader {
    optional string uri = 1;
    optional string method = 3;
    optional int32 status_code = 4;
    optional string content_type = 6;
}
```

Subsequent parts are the payload data.

### Mercury Request/Response

**Sending a request:**
1. Increment the sequence counter
2. Build a `MercuryHeader` protobuf with URI and method
3. Encode into the wire format with the sequence number
4. Send as AP packet type `0xb2` (MercuryReq)
5. Wait for a response packet with the matching sequence number

**Receiving a response:**
1. Read the AP packet
2. Parse the wire format to extract sequence, parts
3. Unmarshal the first part as `MercuryHeader`
4. Match the sequence to the pending request
5. Deliver the response via the pending request's channel

### Mercury Pub/Sub

**Subscribing:**
1. Send a `MercurySub` (0xb3) packet with method "SUB" and the target URI
2. Wait for confirmation (status < 400)
3. Register the subscription for the URI

**Receiving events:**
1. `MercuryEvent` (0xb5) packets arrive asynchronously
2. The URI from the event header is matched against active subscriptions
3. Matching events are delivered to the subscription's channel

**Unsubscribing:**
1. Send a `MercuryUnsub` (0xb4) packet with method "UNSUB"
2. Wait for confirmation
3. Close the subscription channel and remove the registration

### Subscription Matching

Subscriptions are matched by:
1. **Exact URI match:** `event.uri == subscription.uri`
2. **Wildcard prefix:** If the subscription URI ends with `*`, match if the event URI starts with the prefix (everything before the `*`)

Example: subscribing to `hm://connect-state/v1/*` would match events with URIs like `hm://connect-state/v1/cluster` or `hm://connect-state/v1/player`.

---

## Spclient HTTP API

The spclient is Spotify's private HTTP API infrastructure. spotcontrol uses it for connect-state device management, playback control, and metadata retrieval.

### Spclient Authentication Headers

All spclient requests include:

```
Authorization: Bearer <login5_access_token>
Client-Token: <client_token>
User-Agent: spotcontrol/<version> Go/<go-version>
```

Web API requests (to `api.spotify.com`) use the OAuth2 token instead of the Login5 token if available.

### Connect-State Endpoints

#### PUT Device State

```
PUT /connect-state/v1/devices/hobs_{sha1_device_id}

Headers:
    X-Spotify-Connection-Id: <dealer_connection_id>
    Content-Type: application/protobuf

Body: gzip(PutStateRequest protobuf)
Response: Cluster protobuf
```

The device ID in the URL is `hobs_` followed by the SHA-1 hex digest of the raw device ID bytes. This endpoint registers (or updates) the device in the connect-state cluster.

**PutStateRequest:**
```protobuf
message PutStateRequest {
    MemberType member_type = 1;         // CONNECT_STATE
    PutStateReason put_state_reason = 2; // NEW_DEVICE
    Device device = 3;
    uint64 client_side_timestamp = 7;
}
```

#### Player Command

```
POST /connect-state/v1/player/command/from/{from_device}/to/{to_device}

Headers:
    X-Spotify-Connection-Id: <dealer_connection_id>
    Content-Type: application/json

Body: gzip(PlayerCommandRequest JSON)
```

### Player Command Format

The `PlayerCommandRequest` JSON structure matches what the Spotify desktop client sends (captured via mitmproxy):

```json
{
    "command": {
        "endpoint": "resume" | "pause" | "skip_next" | "skip_prev" | "seek_to" | "set_shuffling_context" | "set_repeating_context" | "set_repeating_track" | "play" | "add_to_queue",
        "value": <varies by endpoint>,
        "context": { "uri": "...", "url": "context://..." },
        "play_origin": {
            "feature_identifier": "playlist" | "track" | "npb",
            "feature_version": "spotcontrol/...",
            "referrer_identifier": "your_library"
        },
        "options": {
            "override_restrictions": false,
            "only_for_local_device": false,
            "system_initiated": false,
            "allow_seeking": true
        },
        "play_options": {
            "reason": "interactive",
            "operation": "replace",
            "trigger": "immediately"
        },
        "logging_params": {
            "command_initiated_time": 1718450000000,
            "command_received_time": 1718450000002,
            "page_instance_ids": [],
            "interaction_ids": [],
            "device_identifier": "<device_id>",
            "command_id": "<random_hex>"
        },
        "prepare_play_options": {
            "always_play_something": false,
            "skip_to": { "track_uri": "...", "track_uid": "..." },
            "initially_paused": false,
            "player_options_override": {
                "shuffling_context": false,
                "modes": { "context_enhancement": "NONE" }
            },
            "session_id": "<random_hex>",
            "license": "premium",
            "prefetch_level": "none",
            "audio_stream": "default"
        }
    },
    "connection_type": "wlan",
    "intent_id": "<random_hex>"
}
```

**Command endpoints and their `value` field:**

| Endpoint | Value Type | Description |
|----------|-----------|-------------|
| `resume` | (none) | Resume playback |
| `pause` | (none) | Pause playback |
| `skip_next` | (none) | Skip to next track |
| `skip_prev` | (none) | Skip to previous track |
| `seek_to` | `int64` (ms) | Seek to position |
| `set_shuffling_context` | `bool` | Enable/disable shuffle |
| `set_repeating_context` | `bool` | Enable/disable context repeat |
| `set_repeating_track` | `bool` | Enable/disable track repeat |
| `play` | (complex) | Start playback with full context |
| `add_to_queue` | (track in `track` field) | Add track to queue |

### Volume Signaling

```
PUT /connect-state/v1/connect/volume/from/{from_device}/to/{to_device}

Headers:
    X-Spotify-Connection-Id: <dealer_connection_id>
    Content-Type: application/protobuf

Body: SetVolumeCommand protobuf { volume: <0-65535> }
```

Volume uses the 0–65535 range internally (matching librespot). The controller converts from/to the 0–100 percentage range.

Volume updates are debounced (500ms) to prevent rate limiting during rapid adjustments.

### Transfer Endpoint

```
POST /connect-state/v1/connect/transfer/from/{from_device}/to/{to_device}

Headers:
    X-Spotify-Connection-Id: <dealer_connection_id>
    Content-Type: application/json

Body (optional): TransferRequest JSON
{
    "transfer_options": {
        "restore_paused": "restore"   // only when play=false
    }
}
```

When `play=true`, no body is needed (or an empty body). When `play=false`, the `restore_paused: "restore"` option tells the target device to remain paused.

### Private Metadata API

```
GET /metadata/4/track/{hex_id}?market=from_token

Headers:
    Authorization: Bearer <login5_token>
    Accept: application/json
```

**Response (JSON):**
```json
{
    "gid": "c1c98828fc2c44d6bb247ad01bdb7d4d",
    "name": "Track Title",
    "album": {
        "gid": "...",
        "name": "Album Name",
        "artist": [{"gid": "...", "name": "Album Artist"}],
        "label": "Label Name",
        "date": {"year": 2024, "month": 6, "day": 15},
        "cover_group": {
            "image": [
                {"file_id": "abc123", "size": "SMALL", "width": 64, "height": 64},
                {"file_id": "def456", "size": "DEFAULT", "width": 300, "height": 300},
                {"file_id": "ghi789", "size": "LARGE", "width": 640, "height": 640}
            ]
        }
    },
    "artist": [{"gid": "...", "name": "Artist Name"}],
    "number": 5,
    "disc_number": 1,
    "duration": 234000,
    "popularity": 72,
    "canonical_uri": "spotify:track:...",
    "external_id": [{"type": "isrc", "id": "USRC12345678"}],
    "media_type": "AUDIO"
}
```

Cover art URLs are constructed as: `https://i.scdn.co/image/{file_id}`

This endpoint uses only the Login5 bearer token — no OAuth2 Web API token is required.

### Web API Proxy

The spclient can proxy requests to the Spotify Web API (`api.spotify.com`):

```
{METHOD} /v1/me/player/{endpoint}

# Routed through spclient base URL, but uses the same path as api.spotify.com
```

This avoids the stricter rate limits on the public `api.spotify.com` endpoint. The spclient injects the Login5 bearer token automatically.

For actual `api.spotify.com` requests (e.g. device listing), the `WebApiRequest` method uses the OAuth2 token if available, falling back to the Login5 token with a one-time warning.

### Retry & Rate Limiting

The spclient implements automatic retry for transient errors:

| HTTP Status | Behavior |
|-------------|----------|
| **401 Unauthorized** | Force-refresh the bearer token, retry (max 2 token refreshes) |
| **429 Too Many Requests** | Wait for `Retry-After` header (max 60s, default 5s), retry |
| **503 Service Unavailable** | Same as 429 |

Maximum total retries: 5 per request.

The `Retry-After` header is parsed as either:
- Delta-seconds (e.g. `Retry-After: 5`)
- HTTP-date (e.g. `Retry-After: Sun, 15 Jun 2025 12:00:00 GMT`)

---

## AP Resolver

The AP resolver fetches service endpoint addresses from `https://apresolve.spotify.com/`:

```
GET /?type=accesspoint&type=dealer&type=spclient

Response (JSON):
{
    "accesspoint": [
        "ap-gue1.spotify.com:4070",
        "ap-gew1.spotify.com:4070",
        ...
    ],
    "dealer": [
        "dealer.spotify.com:443",
        ...
    ],
    "spclient": [
        "spclient-wg.spotify.com:443",
        ...
    ]
}
```

**Caching:** Results are cached for 1 hour. The `GetAddressFunc` returned by each `Get*` method rotates through the cached addresses, automatically fetching new ones when the list is exhausted.

---

## Protobuf Messages

spotcontrol uses protobuf for several protocol layers. The `.proto` files and generated Go code are under `proto/spotify/`:

| Package | Purpose |
|---------|---------|
| `spotify` | Core types: `APWelcome`, `LoginCredentials`, `MercuryHeader`, `ClientHello`, `APResponseMessage`, etc. |
| `spotify/clienttoken` | Client token request/response types |
| `spotify/login5/v3` | Login5 request/response types |
| `spotify/login5/v3/credentials` | Credential types (StoredCredential, Password, etc.) |
| `spotify/login5/v3/challenges` | Challenge/solution types (HashcashChallenge, etc.) |
| `spotify/connectstate` | Connect-state types: `Cluster`, `ClusterUpdate`, `PutStateRequest`, `Device`, `PlayerState`, `Context`, etc. |
| `spotify/connectstate/devices` | Device types and enums (`DeviceType`) |

The protobuf code is generated using `protoc-gen-go` v1.34.x compatible with `google.golang.org/protobuf v1.34.2`.

---

## Security Considerations

### Transport Security

| Protocol | Encryption | Details |
|----------|-----------|---------|
| **AP** | Shannon stream cipher | DH key exchange, server signature verification via RSA |
| **Login5** | TLS (HTTPS) | Standard TLS to `login5.spotify.com` |
| **Dealer** | TLS (WSS) | Secure WebSocket to dealer endpoints |
| **Spclient** | TLS (HTTPS) | Standard TLS to spclient endpoints |
| **AP Resolver** | TLS (HTTPS) | Standard TLS to `apresolve.spotify.com` |

### Server Authentication

The AP server authenticates itself by signing its DH public key with a well-known RSA private key. The client verifies this signature using the corresponding RSA public key (2048-bit, hardcoded in `ap/sig.go`). This prevents man-in-the-middle attacks on the AP connection.

### Credential Protection

- **Stored credentials** from APWelcome are opaque blobs that can authenticate without the original password
- **State files** are written with `0600` permissions (owner read/write only)
- **Usernames** are obfuscated in log messages (first 3 chars + `***`)
- **Client ID** (`65b708073fc0480ea92a077233ca87bd`) is the well-known ID used by official Spotify clients — it is not a secret

### Token Types

| Token | Endpoint | Lifetime | Renewal |
|-------|----------|----------|---------|
| Client token | clienttoken.spotify.com | Days | Fetched at session start |
| Login5 token | login5.spotify.com | ~1 hour | Auto-renewed via stored credentials |
| OAuth2 token | accounts.spotify.com | ~1 hour | Auto-renewed via refresh token |

---

## Next Steps

- **[Architecture](architecture.md)** — How these protocols fit into the overall system
- **[Authentication](authentication.md)** — Detailed authentication flow documentation
- **[Package Reference](package-reference.md)** — Full API reference for each package
- **[Controller Guide](controller-guide.md)** — High-level usage of the controller API