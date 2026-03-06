# Contributing

This document covers development setup, testing, project structure, protobuf code generation, and guidelines for contributing to spotcontrol.

## Table of Contents

- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [Building](#building)
- [Testing](#testing)
  - [Running All Tests](#running-all-tests)
  - [Running Specific Package Tests](#running-specific-package-tests)
  - [Test Coverage](#test-coverage)
  - [What's Tested](#whats-tested)
- [Protobuf Generation](#protobuf-generation)
  - [Prerequisites](#protobuf-prerequisites)
  - [Proto File Location](#proto-file-location)
  - [Regenerating Go Code](#regenerating-go-code)
  - [Version Compatibility](#version-compatibility)
- [Code Organization](#code-organization)
  - [Package Responsibilities](#package-responsibilities)
  - [Dependency Direction](#dependency-direction)
  - [Adding a New Package](#adding-a-new-package)
- [Coding Guidelines](#coding-guidelines)
  - [Error Handling](#error-handling)
  - [Logging](#logging-guidelines)
  - [Concurrency](#concurrency)
  - [Naming Conventions](#naming-conventions)
- [Protocol Captures](#protocol-captures)
- [Running the Examples](#running-the-examples)
- [Common Development Tasks](#common-development-tasks)

---

## Development Setup

### Prerequisites

- **Go 1.23** or later
- A **Spotify account** (Free or Premium — some playback control features require Premium)
- Git

### Clone and Build

```sh
git clone https://github.com/mcMineyC/spotcontrol.git
cd spotcontrol
go build ./...
```

### Verify the Setup

```sh
go vet ./...
go test ./...
```

### IDE Setup

spotcontrol is a standard Go module. Any IDE with Go support (VS Code with gopls, GoLand, Zed, etc.) should work out of the box. The module path is `github.com/mcMineyC/spotcontrol`.

---

## Project Structure

```
spotcontrol/
├── docs/                      # Documentation (Markdown)
│   ├── README.md              # Documentation index
│   ├── getting-started.md     # Quick start guide
│   ├── architecture.md        # System design overview
│   ├── authentication.md      # Authentication methods
│   ├── package-reference.md   # API reference for all packages
│   ├── controller-guide.md    # Controller usage guide
│   ├── protocol-details.md    # Wire-level protocol details
│   ├── examples.md            # Example app walkthroughs
│   ├── configuration.md       # All configuration options
│   └── contributing.md        # This file
│
├── ap/                        # Access Point TCP connection
│   ├── accumulator.go         # Connection byte accumulator for DH challenge
│   ├── ap.go                  # Main AP logic: connect, auth, reconnect
│   ├── conn.go                # Protobuf message read/write helpers
│   ├── packets.go             # Packet type constants and Packet struct
│   ├── shannon.go             # Shannon cipher encrypted connection
│   └── sig.go                 # RSA server signature verification
│
├── apresolve/                 # Endpoint resolver
│   ├── resolve.go             # ApResolver: fetch & cache AP/dealer/spclient URLs
│   └── types.go               # Endpoint type constants
│
├── controller/                # High-level playback control API
│   ├── controller.go          # Controller struct, lifecycle, device registration
│   ├── devices.go             # Device listing (cluster cache & Web API)
│   ├── events.go              # Event types, subscriptions, metadata fetching
│   ├── playback.go            # Play, pause, next, prev, seek, shuffle, repeat
│   ├── player_state.go        # Player state from cluster & Web API
│   ├── playlist.go            # PlayPlaylist command
│   ├── transfer.go            # LoadTrack, PlayTrack, AddToQueue, TransferPlayback
│   └── volume.go              # Volume control with debouncing
│
├── cuts/                      # Captured protocol data (from mitmproxy)
│   ├── pause.bin              # Captured pause command
│   ├── pause.buf              # Captured pause command (buffer format)
│   └── play.buf               # Captured play command (buffer format)
│
├── dealer/                    # Dealer WebSocket client
│   ├── dealer.go              # WebSocket connection, ping/pong, reconnection
│   └── msg.go                 # Message types: RawMessage, Message, Request, Reply
│
├── dh/                        # Diffie-Hellman key exchange
│   ├── dh.go                  # DH implementation (768-bit MODP group)
│   └── dh_test.go             # DH tests
│
├── examples/                  # Example applications
│   ├── micro-controller/      # Interactive CLI for playback control
│   │   ├── main.go
│   │   └── README.md
│   └── event-watcher/         # Passive event monitoring
│       ├── main.go
│       └── README.md
│
├── login5/                    # Login5 authentication
│   ├── hashcash.go            # Hashcash challenge solver
│   ├── hashcash_test.go       # Hashcash tests
│   └── login5.go              # Login5 client: auth, token management
│
├── mercury/                   # Mercury (Hermes) pub/sub messaging
│   └── mercury.go             # Client: request/response, subscribe/unsubscribe
│
├── proto/                     # Protobuf definitions and generated Go code
│   └── spotify/               # Spotify-specific proto packages
│       ├── *.proto / *.pb.go  # Core types (APWelcome, LoginCredentials, etc.)
│       ├── clienttoken/       # Client token types
│       ├── connectstate/      # Connect-state types (Cluster, Device, PlayerState)
│       └── login5/            # Login5 types (LoginRequest, challenges, credentials)
│
├── quick/                     # High-level convenience API
│   ├── connect.go             # Connect() one-liner, ConnectResult, pass-throughs
│   └── connect_test.go        # ApplyDefaults tests
│
├── session/                   # Session orchestration
│   ├── client_token.go        # Client token retrieval
│   ├── oauth2.go              # OAuth2 PKCE server
│   ├── options.go             # Options struct, credential types
│   └── session.go             # Session: full connection flow, ExportState
│
├── spclient/                  # Spclient HTTP wrapper
│   ├── metadata.go            # TrackMetadata types, GetTrackMetadata
│   ├── spclient.go            # HTTP client: Request, WebApiRequest, connect-state
│   └── spclient_test.go       # Retry logic tests
│
├── client_id.go               # Spotify client ID constant
├── device.go                  # DeviceType aliases, GenerateDeviceId
├── device_test.go             # Device ID tests
├── go.mod                     # Go module definition
├── go.sum                     # Dependency checksums
├── ids.go                     # SpotifyId, base62/GID conversion utilities
├── ids_test.go                # ID conversion tests
├── logger.go                  # Logger interface, NullLogger, ObfuscateUsername
├── logger_simple.go           # SimpleLogger implementation
├── logger_slog.go             # SlogLogger adapter
├── platform.go                # Platform detection for protobuf fields
├── state.go                   # AppState, SaveState, LoadState
├── state_test.go              # State persistence tests
├── types.go                   # GetAddressFunc, GetLogin5TokenFunc, AppState
├── version.go                 # Version strings, UserAgent, SystemInfo
├── LICENSE                    # MIT License
└── README.md                  # Project README
```

---

## Building

### Build All Packages

```sh
go build ./...
```

### Build Examples

```sh
# micro-controller
cd examples/micro-controller
go build -o micro-controller

# event-watcher
cd examples/event-watcher
go build -o event-watcher
```

### Build with Version Info

```sh
go build -ldflags "-X github.com/mcMineyC/spotcontrol.version=1.0.0" ./examples/micro-controller
```

This sets the version string used in the User-Agent header and device registration.

---

## Testing

### Running All Tests

```sh
go test ./...
```

### Running Specific Package Tests

```sh
# Run tests for a specific package
go test ./dh/
go test ./login5/
go test ./spclient/
go test .  # root package (ids, state, device)

# Run with verbose output
go test -v ./...

# Run a specific test function
go test -v -run TestCheckHashcash ./login5/
```

### Test Coverage

```sh
# Generate coverage report
go test -coverprofile=coverage.out ./...

# View coverage in browser
go tool cover -html=coverage.out

# Print coverage summary
go test -cover ./...
```

### What's Tested

The following packages have unit tests:

| Package | Test File | What's Tested |
|---------|-----------|---------------|
| Root (`spotcontrol`) | `ids_test.go` | Base62/GID conversion, URI parsing, SpotifyId creation |
| Root (`spotcontrol`) | `device_test.go` | Device ID generation, length/format validation |
| Root (`spotcontrol`) | `state_test.go` | SaveState/LoadState round-trip, missing file behavior, file permissions |
| `dh` | `dh_test.go` | DH key generation, key exchange, shared secret computation |
| `login5` | `hashcash_test.go` | Hashcash verification (`checkHashcash`), counter incrementing |
| `spclient` | `spclient_test.go` | Retry logic for 401/429/503 responses, Retry-After header parsing |
| `quick` | `connect_test.go` | `ApplyDefaults` behavior — default values for QuickConfig |

**Note:** Many packages (AP, dealer, session, controller, mercury) require a live Spotify connection and are not covered by automated unit tests. The examples serve as integration tests — see [Running the Examples](#running-the-examples).

### Writing Tests

When adding new functionality:

1. **Pure logic** (ID conversion, hashcash solving, retry logic) — write unit tests
2. **Network-dependent code** (AP connection, dealer, spclient) — consider:
   - Extracting testable logic into separate functions
   - Using interfaces for HTTP clients where possible
   - Adding tests with mock servers if practical
3. **Use `testing.T` helpers** — `t.Helper()`, `t.Fatal()`, `t.Errorf()`
4. **Table-driven tests** where appropriate

---

## Protobuf Generation

### Protobuf Prerequisites

To regenerate the Go code from `.proto` files, you need:

- **`protoc`** — The Protocol Buffer compiler
  ```sh
  # macOS
  brew install protobuf

  # Linux (Ubuntu/Debian)
  apt install protobuf-compiler

  # Or download from https://github.com/protocolbuffers/protobuf/releases
  ```

- **`protoc-gen-go`** — The Go code generator plugin
  ```sh
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
  ```

### Proto File Location

All `.proto` files and their generated `.pb.go` files are under `proto/spotify/`:

```
proto/spotify/
├── keyexchange.proto          # AP key exchange messages
├── authentication.proto       # Login credentials, APWelcome
├── mercury.proto              # Mercury header
├── clienttoken/
│   └── data/v0/
│       └── clienttoken.proto  # Client token types
├── connectstate/
│   ├── connect.proto          # Cluster, Device, PlayerState, PutStateRequest
│   └── devices/
│       └── devices.proto      # DeviceType enum
└── login5/
    └── v3/
        ├── login5.proto       # LoginRequest, LoginResponse
        ├── challenges/
        │   └── challenges.proto  # HashcashChallenge, HashcashSolution
        └── credentials/
            └── credentials.proto  # StoredCredential, Password, etc.
```

### Regenerating Go Code

From the repository root:

```sh
# Regenerate all proto files
protoc --go_out=. --go_opt=paths=source_relative \
    proto/spotify/*.proto \
    proto/spotify/clienttoken/data/v0/*.proto \
    proto/spotify/connectstate/*.proto \
    proto/spotify/connectstate/devices/*.proto \
    proto/spotify/login5/v3/*.proto \
    proto/spotify/login5/v3/challenges/*.proto \
    proto/spotify/login5/v3/credentials/*.proto
```

Or regenerate individual proto files:

```sh
# Just the connect-state types
protoc --go_out=. --go_opt=paths=source_relative \
    proto/spotify/connectstate/*.proto
```

### Version Compatibility

The generated code must be compatible with the `google.golang.org/protobuf` version in `go.mod`:

| Dependency | Version | Notes |
|------------|---------|-------|
| `google.golang.org/protobuf` | v1.34.2 | Runtime library |
| `protoc-gen-go` | v1.34.x | Must match runtime major/minor |
| `protoc` | v3.x or v4.x | Any recent version |

**Important:** If you update the protobuf runtime in `go.mod`, also update `protoc-gen-go` to a compatible version to avoid compatibility issues.

### Checking for Drift

After regenerating, check for unexpected changes:

```sh
git diff proto/
```

If there are no changes, the generated code is up to date. If there are changes, review them carefully — they may indicate a version mismatch.

---

## Code Organization

### Package Responsibilities

Each package has a focused responsibility:

| Package | Responsibility | Depends On |
|---------|---------------|------------|
| Root (`spotcontrol`) | Shared types, IDs, logging, state, constants | Proto |
| `ap` | AP TCP connection, DH, Shannon, packets | Root, `dh`, proto |
| `dh` | Diffie-Hellman key exchange | (none) |
| `apresolve` | Service endpoint resolution | Root |
| `login5` | Login5 authentication, hashcash | Root, proto |
| `mercury` | Mercury pub/sub over AP | Root, `ap`, proto |
| `spclient` | HTTP wrapper for spclient/Web API | Root, proto |
| `dealer` | Dealer WebSocket | Root, proto |
| `controller` | High-level playback control | Root, `spclient`, `dealer`, proto |
| `session` | Session orchestration | All of the above |
| `quick` | Convenience `Connect()` wrapper | Root, `session`, `controller` |

### Dependency Direction

Dependencies flow **upward** in the stack:

```
quick → session → controller → spclient, dealer
                 → ap → dh
                 → mercury → ap
                 → login5
                 → apresolve
```

**Rules:**

- Lower-level packages (`ap`, `dh`, `login5`, `mercury`) do NOT depend on higher-level packages (`session`, `controller`, `quick`)
- The root package (`spotcontrol`) is imported by all sub-packages for shared types
- Proto packages are imported by packages that need protobuf types
- `controller` depends on `spclient` and `dealer` but NOT on `session`
- `session` depends on everything and ties it all together

### Adding a New Package

1. Create a new directory under `spotcontrol/`
2. Use the import path `github.com/mcMineyC/spotcontrol/<package_name>`
3. Accept a `spotcontrol.Logger` in the constructor for consistent logging
4. Follow the existing patterns for error handling, nil-safety, and concurrency
5. Add tests in `<package_name>_test.go`
6. Document exported types and functions with Go doc comments

---

## Coding Guidelines

### Error Handling

- **Wrap errors** with context using `fmt.Errorf("descriptive message: %w", err)`
- **Don't swallow errors** — log them at minimum
- **Permanent vs. transient** — use `backoff.Permanent(err)` for errors that should not be retried (e.g. login failures)
- **Return early** — check errors immediately and return, don't nest deeply
- **Deferred cleanup** — use `defer` for `Close()` calls: `defer func() { _ = resp.Body.Close() }()`

```go
// Good: wrapped error with context
resp, err := c.client.Do(req)
if err != nil {
    return nil, fmt.Errorf("failed requesting login5 endpoint: %w", err)
}

// Good: permanent error (don't retry)
if _, ok := err.(*AccesspointLoginError); ok {
    return backoff.Permanent(err)
}
```

### Logging Guidelines

- Use the `spotcontrol.Logger` interface throughout — never `fmt.Println` or `log.Printf` in library code
- **Trace** — packet-level details, ping/pong events (very noisy)
- **Debug** — operational details: token renewal, connection state, key exchange steps
- **Info** — significant events: session established, device registered, authentication success
- **Warn** — recoverable issues: fallback to Web API, dropped events, missing tokens
- **Error** — failures that affect functionality: connection loss, failed commands
- **Always obfuscate usernames** in log messages: `spotcontrol.ObfuscateUsername(username)`
- **Never log** full tokens, credentials, or other secrets

```go
// Good: obfuscated username, appropriate level
c.log.WithField("username", spotcontrol.ObfuscateUsername(username)).
    Infof("authenticated Login5")

// Good: structured context
c.log.WithError(err).Warnf("connect-state play playlist failed, falling back to Web API")

// Bad: leaking secrets
c.log.Infof("token: %s", accessToken)  // NEVER DO THIS
```

### Concurrency

- **Protect shared state** with `sync.RWMutex` — use read locks for reads, write locks for writes
- **Use channels** for cross-goroutine communication (event subscriptions, request/response patterns)
- **Non-blocking sends** on buffered channels — use `select` with `default` to drop rather than block:
  ```go
  select {
  case ch <- event:
  default:
      c.log.Debugf("dropping event: channel full")
  }
  ```
- **Close channels** when the producer is done (controller close, dealer close)
- **`sync.Once`** for one-time initialization (starting receive loops)
- **Independent locks** for independent state (separate mutexes for cluster state, connection ID, volume debounce, etc.)

### Naming Conventions

- Follow standard Go naming conventions
- Exported types have clear, descriptive names: `Controller`, `PlayerState`, `TrackMetadata`
- Unexported helper functions are prefixed with the action: `handleClusterUpdate`, `sendVolumeNow`, `fetchAndEmitMetadata`
- Options structs are named `*Options`: `LoadTrackOptions`, `PlayTrackOptions`, `PlayPlaylistOptions`
- Event types end with `Event`: `DeviceListEvent`, `PlaybackEvent`, `MetadataEvent`
- Config structs are named `Config`: `Config` (controller), `QuickConfig` (quick)

---

## Protocol Captures

The `cuts/` directory contains binary captures of Spotify desktop client traffic obtained via mitmproxy. These were used to understand the wire format of connect-state player commands.

| File | Description |
|------|-------------|
| `pause.bin` | Captured pause command |
| `pause.buf` | Captured pause command (buffer format) |
| `play.buf` | Captured play command (buffer format) |

These files serve as reference for the expected command format. When implementing new connect-state commands:

1. Capture the command from the Spotify desktop client using mitmproxy
2. Save the capture to `cuts/` for reference
3. Implement the command in `controller/` matching the captured format
4. Test against a real Spotify session

---

## Running the Examples

### micro-controller

```sh
cd examples/micro-controller
go build -o micro-controller

# First run: interactive OAuth2 login
./micro-controller --interactive

# Subsequent runs: uses stored credentials
./micro-controller
```

See the [Examples documentation](examples.md#micro-controller) for a full walkthrough.

### event-watcher

```sh
cd examples/event-watcher
go build -o event-watcher

# First run: interactive OAuth2 login
./event-watcher --interactive

# Subsequent runs: uses stored credentials
./event-watcher
```

See the [Examples documentation](examples.md#event-watcher) for details.

### State File

Both examples use `spotcontrol_state.json` in their respective directories by default. After the first interactive login, credentials are saved automatically. Delete this file to force re-authentication.

**Security note:** The state file contains authentication credentials. Do not commit it to version control. The `.gitignore` should exclude `*_state.json` files.

---

## Common Development Tasks

### Adding a New Playback Command

1. **Capture the command** from the Spotify desktop client via mitmproxy (optional but recommended)
2. **Add the method** to `controller/playback.go` (or a new file if it's a distinct feature)
3. **Follow the pattern**: connect-state command first, Web API fallback
4. **Use helper methods**: `sendPlayerCommand`, `newCommandRequest`, `newLoggingParams`, `newCommandOptions`
5. **Add pass-through** to `quick/connect.go` in the `ConnectResult` type
6. **Update documentation**: add the method to `controller-guide.md` and `package-reference.md`

### Adding a New Event Type

1. **Define the event struct** in `controller/events.go`
2. **Add a channel slice** to the `eventSubscribers` struct
3. **Add `Subscribe*`** method to `Controller`
4. **Add `emit*`** helper method
5. **Close channels** in `closeAllSubscribers`
6. **Emit events** from the appropriate place in the cluster update processing
7. **Add pass-through** to `quick/connect.go`
8. **Update documentation**

### Adding a New Credential Type

1. **Add a struct** in `session/options.go` implementing the `Credentials` interface
2. **Add a case** in the credential switch in `session.NewSessionFromOptions`
3. **Implement the AP connection** method if needed (in `ap/ap.go`)
4. **Update documentation**: `authentication.md` and `package-reference.md`

### Updating Dependencies

```sh
# Update all dependencies
go get -u ./...
go mod tidy

# Update a specific dependency
go get -u github.com/coder/websocket@latest
go mod tidy

# Verify no breaking changes
go build ./...
go test ./...
```

### Debugging Protocol Issues

1. **Enable trace logging** for maximum detail:
   ```go
   log := spotcontrol.NewSimpleLogger(os.Stderr)
   // Trace messages are suppressed by SimpleLogger; use SlogLogger with debug level
   // or a custom logger that prints trace messages
   ```

2. **Use mitmproxy** to capture spclient/Login5/dealer traffic:
   ```sh
   mitmproxy --mode regular --listen-port 8080
   ```
   Configure spotcontrol to use the proxy via a custom `http.Client` with a proxy transport.

3. **Check the dealer WebSocket** messages by logging raw JSON in `dealer.go`

4. **Examine AP packets** by adding logging in `ap/shannon.go` before/after encryption

---

## Next Steps

- **[Architecture](architecture.md)** — Understand the system design
- **[Protocol Details](protocol-details.md)** — Low-level protocol internals
- **[Package Reference](package-reference.md)** — Full API reference
- **[Examples](examples.md)** — Example application walkthroughs