# Authentication

spotcontrol supports multiple authentication methods for connecting to Spotify's backend. This document covers each method, how tokens are managed, and how credentials are persisted across sessions.

## Authentication Architecture

Spotify uses a multi-layered authentication system. spotcontrol must authenticate with several services during startup:

```
1. Client Token        ← clienttoken.spotify.com (HTTPS, protobuf)
2. Access Point (AP)   ← TCP connection with DH key exchange + credentials
3. Login5              ← login5.spotify.com (HTTPS, protobuf, uses stored creds from AP)
4. OAuth2 Web API      ← accounts.spotify.com (HTTPS, PKCE flow, optional)
```

Each layer produces tokens or credentials used by subsequent layers:

| Layer | Produces | Used By |
|-------|----------|---------|
| Client Token | Client token string | Login5, Spclient (injected as `Client-Token` header) |
| AP Authentication | `APWelcome` with reusable stored credentials | Login5 (as `StoredCredential`) |
| Login5 | Bearer access token (short-lived) | Spclient, Dealer (as `Authorization: Bearer` header) |
| OAuth2 PKCE | OAuth2 access + refresh tokens | Web API requests to `api.spotify.com` |

## Credential Types

spotcontrol provides four credential types, all implementing the `session.Credentials` interface:

### 1. Interactive Credentials (OAuth2 PKCE)

The recommended method for first-time authentication. Launches a browser-based OAuth2 PKCE flow.

```go
creds := session.InteractiveCredentials{
    CallbackPort: 0, // 0 = random available port
}
```

**How it works:**

1. A local HTTP server starts on `127.0.0.1:{port}` to receive the OAuth2 callback.
2. An authorization URL is printed to the console (and to the logger).
3. The user opens the URL in their browser and logs in to Spotify.
4. Spotify redirects back to the local server with an authorization code.
5. The code is exchanged for OAuth2 tokens (access token + refresh token).
6. The OAuth2 access token is used to authenticate with the AP via `AUTHENTICATION_SPOTIFY_TOKEN`.
7. The AP returns an `APWelcome` containing reusable stored credentials.

**OAuth2 Scopes requested:**

The interactive flow requests a comprehensive set of scopes:

- `app-remote-control` — Remote control of Spotify Connect devices
- `streaming` — Audio streaming
- `user-modify-playback-state` — Control playback (play, pause, skip, etc.)
- `user-read-playback-state` — Read current playback state
- `user-read-currently-playing` — Read currently playing track
- `user-read-recently-played` — Read recently played tracks
- `user-read-private` — Read user's subscription details
- `user-read-email` — Read user's email
- `user-library-read` / `user-library-modify` — Read/modify saved tracks and albums
- `user-follow-read` / `user-follow-modify` — Read/modify followed artists and users
- `user-top-read` — Read user's top artists and tracks
- `playlist-read-private` / `playlist-read-collaborative` — Read playlists
- `playlist-modify-public` / `playlist-modify-private` — Modify playlists
- And several others for full API coverage

**After authentication**, you should save the session state so subsequent runs can use stored credentials instead of re-launching the browser flow:

```go
sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    DeviceType:  spotcontrol.DeviceTypeComputer,
    DeviceName:  "MyApp",
    Credentials: session.InteractiveCredentials{CallbackPort: 0},
})
if err != nil {
    log.Fatal(err)
}

// Save everything for next time
state := sess.ExportState()
if err := spotcontrol.SaveState("state.json", state); err != nil {
    log.Printf("warning: %v", err)
}
```

### 2. Stored Credentials

The most common method for subsequent sessions after the first interactive login. Uses the reusable authentication credentials from a previous `APWelcome`.

```go
creds := session.StoredCredentials{
    Username: "spotify_username",
    Data:     storedCredentialBytes, // from APWelcome.ReusableAuthCredentials
}
```

**How it works:**

1. The stored credential bytes are sent to the AP as `AUTHENTICATION_STORED_SPOTIFY_CREDENTIALS`.
2. The AP validates them and returns a new `APWelcome` (with potentially refreshed stored credentials).
3. Login5 is then authenticated using the stored credentials from the new `APWelcome`.

**Typical usage with state persistence:**

```go
state, err := spotcontrol.LoadState("state.json")
if err != nil {
    log.Fatal(err)
}

sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    DeviceType: spotcontrol.DeviceTypeComputer,
    DeviceId:   state.DeviceId,
    DeviceName: "MyApp",
    Credentials: session.StoredCredentials{
        Username: state.Username,
        Data:     state.StoredCredentials,
    },
    AppState: state, // Also restores the OAuth2 token
})
```

### 3. Spotify Token Credentials

Authenticates with a Spotify OAuth access token obtained through some external means.

```go
creds := session.SpotifyTokenCredentials{
    Username: "spotify_username",
    Token:    "BQD...access_token...",
}
```

**How it works:**

1. The token is sent to the AP as `AUTHENTICATION_SPOTIFY_TOKEN`.
2. The AP validates the token and returns an `APWelcome`.

This is useful if you have another system that already handles Spotify OAuth and you want to pass the token to spotcontrol.

### 4. Blob Credentials

Authenticates using an encrypted discovery blob obtained via Spotify Connect zeroconf discovery.

```go
creds := session.BlobCredentials{
    Username: "spotify_username",
    Blob:     base64EncodedBlobBytes,
}
```

**How it works:**

1. The base64-encoded blob is decoded.
2. A PBKDF2-derived AES key (from SHA-1 of the device ID + username, 256 iterations) decrypts the blob.
3. The decrypted blob contains an authentication type and auth data.
4. These are sent to the AP as the appropriate `LoginCredentials`.

This method is used when another Spotify client discovers this device via mDNS/zeroconf and sends an encrypted blob for authentication.

## Token Lifecycle

### Client Token

- **Obtained from**: `https://clienttoken.spotify.com/v1/clienttoken`
- **Request format**: Protobuf `ClientTokenRequest` containing client ID, client version, and platform-specific data
- **Lifetime**: Long-lived (typically valid for days)
- **Renewal**: Not automatically renewed; a new one is fetched at session creation
- **Usage**: Included as `Client-Token` header in Login5 and Spclient requests
- **Notes**: If the server responds with a challenge (e.g. hashcash or JS evaluation), an error is returned — challenge solving for client tokens is not currently supported

### Login5 Access Token

- **Obtained from**: `https://login5.spotify.com/v3/login`
- **Request format**: Protobuf `LoginRequest` with stored credentials and client info
- **Lifetime**: Short-lived (typically 1 hour, specified in `access_token_expires_in`)
- **Renewal**: Automatic and transparent. The `AccessToken()` function returns a `GetLogin5TokenFunc` that checks expiry and re-authenticates using stored credentials when needed
- **Usage**: Bearer token for Spclient API and Dealer WebSocket authentication
- **Challenge solving**: Hashcash challenges are solved automatically. The algorithm:
  1. Concatenate the challenge prefix with a 16-byte suffix (seeded from SHA-1 of the login context)
  2. Repeatedly SHA-1 hash until the result has the required number of trailing zero bits
  3. Return the suffix and computation duration

### OAuth2 Token (Web API)

- **Obtained from**: Spotify's OAuth2 endpoint via the PKCE authorization code flow
- **Lifetime**: Typically 1 hour
- **Renewal**: Automatic via the refresh token. The `WebApiToken()` function returns a `GetLogin5TokenFunc` that refreshes automatically using `golang.org/x/oauth2`
- **Usage**: Bearer token for Spotify Web API requests (`api.spotify.com`)
- **Important distinction**: The Login5 token does NOT work for `api.spotify.com` endpoints. The OAuth2 token carries the scopes needed by the public Web API. If no OAuth2 token is available, Web API requests fall back to the Login5 token with a warning logged (this will likely fail with scope errors)

### Two-Token System

spotcontrol uses two separate token systems because they serve different purposes:

| | Login5 Token | OAuth2 Token |
|---|---|---|
| **Endpoint** | `login5.spotify.com` | `accounts.spotify.com` |
| **Used for** | Spclient private API, Dealer auth | Public Web API (`api.spotify.com`) |
| **Scopes** | N/A (internal protocol) | Explicit OAuth2 scopes |
| **Renewal** | Re-login with stored credentials | Refresh token exchange |
| **Required?** | Always | Only for Web API operations |

The `Spclient` struct has two token functions:

- `accessToken` — The Login5 token, used for spclient endpoint requests
- `webApiToken` — The OAuth2 token (optional), used for `api.spotify.com` requests

When `webApiToken` is not set, Web API requests fall back to `accessToken` with a one-time warning.

## State Persistence

### `AppState` Structure

The `AppState` struct captures everything needed to restore a session:

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

### Saving State

```go
state := sess.ExportState()
err := spotcontrol.SaveState("state.json", state)
```

`SaveState` writes pretty-printed JSON with file mode `0600` (owner read/write only) to protect credentials:

```json
{
  "device_id": "a1b2c3d4e5f6a1b2c3d4a1b2c3d4e5f6a1b2c3d4",
  "username": "spotify_user",
  "stored_credentials": "base64_encoded_bytes...",
  "oauth_access_token": "BQD...",
  "oauth_refresh_token": "AQB...",
  "oauth_token_type": "Bearer",
  "oauth_expiry": "2025-06-15T12:00:00Z"
}
```

### Loading State

```go
state, err := spotcontrol.LoadState("state.json")
// state is nil (not an error) if file doesn't exist
// state is non-nil if successfully loaded
// err is non-nil only for read/parse errors
```

### Restoring OAuth2 Tokens

When creating a session with `AppState`, the OAuth2 token is automatically restored:

```go
sess, err := session.NewSessionFromOptions(ctx, &session.Options{
    // ... other options ...
    AppState: state, // Restores OAuthAccessToken, OAuthRefreshToken, etc.
})
```

The restored token is used for Web API requests. If the token has expired, it is automatically refreshed using the refresh token. The session constructs a minimal `oauth2.Config` (with just the client ID and Spotify endpoint) for the refresh — the redirect URL and scopes are not needed for token refresh.

## Security Considerations

### Credential Storage

- **File permissions**: `SaveState` uses `0600` permissions, meaning only the file owner can read or write the file.
- **Sensitive data**: The state file contains stored credentials and OAuth2 tokens. Treat it like a password file.
- **No encryption**: The state file is plain JSON. If you need encryption at rest, wrap `SaveState`/`LoadState` with your own encryption layer.

### Network Security

- **AP connection**: Encrypted with Shannon stream cipher after DH key exchange. The server's DH public key is verified against a well-known RSA public key.
- **Login5**: Standard HTTPS (TLS).
- **Dealer**: Secure WebSocket (`wss://`).
- **Spclient**: Standard HTTPS (TLS).
- **Client ID**: The client ID (`65b708073fc0480ea92a077233ca87bd`) is the well-known ID used by official Spotify clients. It is not a secret.

### Username Obfuscation

spotcontrol provides `ObfuscateUsername()` for safe logging — it shows only the first 3 characters followed by `***`:

```go
spotcontrol.ObfuscateUsername("johnsmith") // → "joh***"
spotcontrol.ObfuscateUsername("ab")        // → "***"
```

All built-in log messages use this function when logging usernames.

## Authentication Decision Tree

Use this to decide which credential type to use:

```
Is this the first time running?
├── YES → Do you want browser-based login?
│         ├── YES → InteractiveCredentials
│         └── NO  → Do you have an external OAuth token?
│                   ├── YES → SpotifyTokenCredentials
│                   └── NO  → Do you have a zeroconf blob?
│                             ├── YES → BlobCredentials
│                             └── NO  → InteractiveCredentials (only option)
│
└── NO  → Do you have a saved state file?
          ├── YES → StoredCredentials (from state.Username + state.StoredCredentials)
          └── NO  → Treat as first time (see above)
```

The `quick.Connect()` function implements this decision tree automatically:

1. If `StatePath` is set and the file exists with valid stored credentials → `StoredCredentials`
2. Else if `Interactive` is `true` → `InteractiveCredentials`
3. Else → error ("no credentials available")

## Troubleshooting

### "no OAuth2 token available (interactive login required)"

This means the spclient is trying to make a Web API request (`api.spotify.com`) but no OAuth2 token exists. Run with `Interactive: true` at least once to obtain an OAuth2 token, then save the state.

### "failed authenticating with login5: INVALID_CREDENTIALS"

The stored credentials have expired or been revoked. Delete the state file and re-authenticate interactively.

### "accesspoint login failed: BAD_CREDENTIALS"

Similar to above — the credentials used for the AP connection are invalid. This can happen if:
- The stored credentials are corrupted
- The Spotify password was changed
- The account was locked or suspended

### "clienttoken challenge not supported"

The client token endpoint responded with a challenge (hashcash or JS evaluation) instead of granting a token. This is rare and typically indicates rate limiting. Wait and try again.

### "failed obtaining dealer access token"

The Login5 token renewal failed. This usually means the stored credentials are invalid. Re-authenticate interactively.

### Web API requests failing with 401 but spclient requests work

This indicates the OAuth2 token has expired and cannot be refreshed. The Login5 token (used for spclient) is renewed independently and may still be valid. Re-authenticate interactively to obtain a fresh OAuth2 token with a new refresh token.

## Next Steps

- **[Getting Started](getting-started.md)** — Quick start guide with code examples
- **[Configuration](configuration.md)** — All session and controller configuration options
- **[Protocol Details](protocol-details.md)** — Wire-level protocol details for AP and Login5