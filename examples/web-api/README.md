# web-api

A small JSON HTTP API for controlling Spotify Connect devices using the spotcontrol library. This is the HTTP equivalent of `examples/micro-controller` — every command that CLI offers at its `>>>` prompt is available here as an endpoint you can hit with `curl`, a phone shortcut, a home-automation system, or any other HTTP client.

## Build

```sh
cd examples/web-api
go build -o web-api
```

## Usage

### First Run (Interactive OAuth2 Login)

```sh
./web-api --interactive
```

### Subsequent Runs

```sh
./web-api
```

### Flags

| Flag             | Default                    | Description                                    |
|------------------|-----------------------------|-------------------------------------------------|
| `--interactive`  | `false`                     | Use interactive OAuth2 PKCE login flow          |
| `--state`        | `spotcontrol_state.json`    | Path to state file                              |
| `--devicename`   | `SpotControl Web API`       | Name shown in Spotify Connect device lists      |
| `--callback-port`| `0`                         | OAuth2 callback port (0 = random)               |
| `--addr`         | `:8080`                     | Address the HTTP API listens on                 |

Once it's running you'll see:

```
Connected as: username
Device ID: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
Listening on :8080
See README.md for the full list of endpoints.
```

## Endpoints

All `POST` endpoints accept a JSON body (all fields optional unless noted) and return `{"ok": true}` on success or `{"error": "..."}` with a non-2xx status on failure. Where a `device_id` field is present, an empty string (or omitting it) targets the currently active device.

Response bodies for `GET` endpoints are the raw Go structs from the `controller` package JSON-encoded — field names are capitalized (e.g. `IsPlaying`, `TrackURI`) since the library doesn't define `json` tags on them.

### Status

| Method | Path             | Description                              |
|--------|------------------|-------------------------------------------|
| GET    | `/health`        | Connection status, username, device ID    |

```sh
curl http://localhost:8080/health
```

### Devices

| Method | Path             | Description                                             |
|--------|------------------|-----------------------------------------------------------|
| GET    | `/devices`       | Devices from the cached cluster state (instant)            |
| GET    | `/devices/api`   | Devices queried live from the Spotify Web API               |

```sh
curl http://localhost:8080/devices
```

### Player state & metadata

| Method | Path                        | Description                                                        |
|--------|-----------------------------|----------------------------------------------------------------------|
| GET    | `/state`                    | Current playback state (cluster cache, falls back to Web API)         |
| GET    | `/metadata`                 | Cached rich metadata for the currently playing track                  |
| GET    | `/metadata/fetch?uri=...`   | Fetch rich metadata for a track URI (or current track if `uri` omitted) |

```sh
curl http://localhost:8080/state
curl http://localhost:8080/metadata
curl "http://localhost:8080/metadata/fetch?uri=spotify:track:6rqhFgbbKwnb9MLmUQDhG6"
```

### Transport controls

| Method | Path         | Body                                                | Description                  |
|--------|--------------|------------------------------------------------------|-------------------------------|
| POST   | `/play`      | `{"device_id": ""}`                                   | Resume playback               |
| POST   | `/pause`     | `{"device_id": ""}`                                   | Pause playback                |
| POST   | `/next`      | `{"device_id": ""}`                                   | Skip to next track            |
| POST   | `/previous`  | `{"device_id": ""}`                                   | Skip to previous track        |
| POST   | `/seek`      | `{"position_ms": 30000, "device_id": ""}`             | Seek to a position             |
| POST   | `/volume`    | `{"volume": 50, "device_id": ""}`                     | Set volume (0-100)             |
| POST   | `/shuffle`   | `{"state": true, "device_id": ""}`                    | Toggle shuffle                 |
| POST   | `/repeat`    | `{"state": "context", "device_id": ""}`               | Set repeat: `off`/`context`/`track` |
| POST   | `/queue`     | `{"uri": "spotify:track:...", "device_id": ""}`       | Add a track to the queue        |
| POST   | `/transfer`  | `{"device_id": "abc123", "play": true}`               | Transfer playback to a device  |

```sh
curl -X POST http://localhost:8080/play
curl -X POST http://localhost:8080/pause
curl -X POST http://localhost:8080/volume -d '{"volume": 40}'
curl -X POST http://localhost:8080/seek -d '{"position_ms": 60000}'
curl -X POST http://localhost:8080/shuffle -d '{"state": true}'
curl -X POST http://localhost:8080/repeat -d '{"state": "track"}'
curl -X POST http://localhost:8080/transfer -d '{"device_id": "abc123", "play": true}'
```

### Loading / playing content

| Method | Path              | Body                                                                                  | Description                                                    |
|--------|-------------------|------------------------------------------------------------------------------------------|-------------------------------------------------------------------|
| POST   | `/open`           | `{"url": "https://open.spotify.com/track/...", "device_id": ""}`                        | Play a Spotify open.spotify.com URL (track, playlist, or album)   |
| POST   | `/load`           | `{"uris": ["spotify:track:..."], "context_uri": "", "offset_uri": "", "offset_position": null, "position_ms": 0, "device_id": ""}` | Load track(s) via the Web API                                     |
| POST   | `/play-track`     | `{"uris": ["spotify:track:..."], "device_id": "", "shuffle": false, "skip_to_uri": "", "skip_to_index": null}` | Play track(s) directly via connect-state (no context/recommendations) |
| POST   | `/play-playlist`  | `{"playlist_id": "5ese9XhQqKHoQg4WJ4sZef", "device_id": "", "shuffle": false, "skip_to_track_uri": "", "skip_to_track_uid": ""}` | Play a playlist by ID                                              |

```sh
curl -X POST http://localhost:8080/open \
  -d '{"url": "https://open.spotify.com/track/2AX9H0uIFZqo9zAcwclQy9"}'

curl -X POST http://localhost:8080/play-track \
  -d '{"uris": ["spotify:track:6rqhFgbbKwnb9MLmUQDhG6"]}'

curl -X POST http://localhost:8080/play-playlist \
  -d '{"playlist_id": "5ese9XhQqKHoQg4WJ4sZef", "shuffle": true}'
```

`uris`/`uri` values may be given as either a full `spotify:track:...` URI or a bare base62 ID — bare IDs are automatically prefixed with `spotify:track:`.

### Live events (Server-Sent Events)

| Method | Path      | Description                                                                 |
|--------|-----------|---------------------------------------------------------------------------------|
| GET    | `/events` | Streams device list, playback, and metadata changes as they happen (SSE)         |

This is the HTTP equivalent of the CLI's `watch` command. Each event is a JSON object with a `type` field (`device_list`, `playback`, or `metadata`) and a `data` field holding the corresponding event payload from the `controller` package.

```sh
curl -N http://localhost:8080/events
```

```
data: {"type":"device_list","data":{"Devices":[...],"DevicesThatChanged":[...],"Reason":"NEW_DEVICE_APPEARED"}}

data: {"type":"playback","data":{"State":{"IsPlaying":true,"TrackURI":"spotify:track:...","PositionMs":1234,"DurationMs":234000,...}}}

data: {"type":"metadata","data":{"Metadata":{"Title":"...","Artist":"...","Album":"...","ImageURL":"...",...}}}
```

The connection stays open until the client disconnects; a `: heartbeat` comment is sent every 30 seconds to keep intermediate proxies from closing an idle connection.

## How It Works

This example wires the HTTP layer directly on top of `quick.ConnectResult` / `controller.Controller` — one call per endpoint, no extra abstraction:

1. **Authentication**: same as `micro-controller` — OAuth2 PKCE (`--interactive`) on first run, then stored credentials from `spotcontrol_state.json`.
2. **Routing**: a standard library `net/http.ServeMux` using Go 1.22+ method+path patterns (`"POST /play"`, `"GET /devices"`, etc.) — no external router dependency.
3. **Commands**: each `POST` handler decodes a small JSON request struct and calls straight through to the matching `Controller`/`ConnectResult` method (`Play`, `Pause`, `SetVolume`, `PlayTrack`, `PlayPlaylist`, ...).
4. **Events**: `/events` subscribes to the same `SubscribeDeviceList` / `SubscribePlayback` / `SubscribeMetadata` channels used by `event-watcher`, and re-emits them as Server-Sent Events.

## CORS

Permissive CORS headers (`Access-Control-Allow-Origin: *`) are enabled by default so you can call the API from a browser-based tool during local development. Remove or tighten `withCORS` in `main.go` before exposing this on anything but localhost — there's no authentication on these endpoints, so anyone who can reach the port can control playback.
