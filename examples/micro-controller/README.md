# micro-controller

A command-line interface for controlling Spotify Connect devices using the modernized spotcontrol library.

## Build

```sh
cd examples/micro-controller
go build -o micro-controller
```

## Usage

### First Run (Interactive OAuth2 Login)

On first run, use the `--interactive` flag to authenticate via OAuth2 PKCE in your browser. Credentials are saved automatically to `spotcontrol_state.json` for subsequent runs.

```sh
./micro-controller --interactive
```

### Subsequent Runs

Once credentials are saved, just run without any flags:

```sh
./micro-controller
```

### Custom State File

```sh
./micro-controller --state /path/to/state.json
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--interactive` | `false` | Use interactive OAuth2 PKCE login flow |
| `--state` | `spotcontrol_state.json` | Path to state file (device ID, credentials, OAuth2 tokens) |
| `--devicename` | `SpotControl` | Name shown in Spotify Connect device lists |
| `--port` | `0` | OAuth2 callback port (0 = random available port) |

## Commands

Once running, the following commands are available at the `>>>` prompt:

| Command | Description |
|---------|-------------|
| `load <uri1> [uri2 ...]` | Load and play tracks by Spotify URI (e.g. `spotify:track:6rqhFgbbKwnb9MLmUQDhG6`) |
| `play` | Resume playback |
| `pause` | Pause playback |
| `next` | Skip to next track |
| `prev` | Skip to previous track |
| `volume <0-100>` | Set volume percentage |
| `seek <ms>` | Seek to position in milliseconds |
| `shuffle <on\|off>` | Toggle shuffle mode |
| `repeat <off\|context\|track>` | Set repeat mode |
| `queue <uri>` | Add a track to the playback queue |
| `transfer <device_id>` | Transfer playback to another device |
| `devices` | List devices from the cached cluster state |
| `apidevices` | List devices from the Spotify Web API |
| `state` | Show current player state from the Web API |
| `select` | Interactively choose a target device |
| `help` | Show available commands |
| `quit` | Exit the application |

## How It Works

1. **Authentication**: The app authenticates via OAuth2 PKCE (interactive) or stored credentials from a previous session.
2. **Session**: A `session.Session` is created which connects to the Spotify access point (AP), authenticates with Login5, and initializes the spclient and dealer WebSocket.
3. **Controller**: A `controller.Controller` subscribes to Connect State cluster updates via the dealer and provides high-level playback control methods via the Spotify Web API.
4. **Credentials Persistence**: After successful login, the reusable stored credentials and device ID are saved to a JSON file so the user doesn't need to re-authenticate on subsequent runs.

## Example Session

```
$ ./micro-controller --interactive
[INF] connecting to Spotify...
[INF] to complete authentication, visit the following URL in your browser:
https://accounts.spotify.com/authorize?...

Open this URL in your browser to log in:
https://accounts.spotify.com/authorize?...

[INF] received oauth2 authorization code
[INF] authenticated AP
[INF] authenticated Login5
[INF] session established for user: abc***
[INF] saved credentials to spotcontrol_state.json
[INF] device ID: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2

Available commands:
  load <uri1> [uri2 ...] : load track(s) by Spotify URI
  play                   : resume playback
  ...

>>> apidevices

Devices (from Web API):
  - Living Room Speaker (Speaker) id=abc123 vol=45% [ACTIVE]
  - Desktop (Computer) id=def456 vol=100%

>>> load spotify:track:6rqhFgbbKwnb9MLmUQDhG6
Loading tracks...

>>> state

Player State:
  Playing:  true
  Track:    spotify:track:6rqhFgbbKwnb9MLmUQDhG6
  Position: 1234ms / 234000ms
  Device:   abc123
  Shuffle:  false
  Repeat:   context=false track=false

>>> pause
Paused

>>> quit
Goodbye!
```
