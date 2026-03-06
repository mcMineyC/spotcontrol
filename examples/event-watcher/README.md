# event-watcher

A minimal example that subscribes to the three event channels provided by the spotcontrol controller:

- **Device list changes** — devices appearing, disappearing, or changing state
- **Playback changes** — play/pause, track change, seek, shuffle/repeat toggles
- **Metadata changes** — rich track metadata (title, artist, album, cover art) fetched automatically from Spotify's private metadata API when the track changes

## Build

```sh
cd examples/event-watcher
go build -o event-watcher
```

## Usage

### First Run (Interactive OAuth2 Login)

On first run, use `--interactive` to authenticate via OAuth2 PKCE in your browser. Credentials are saved to `spotcontrol_state.json` for subsequent runs.

```sh
./event-watcher --interactive
```

### Subsequent Runs

```sh
./event-watcher
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--interactive` | `false` | Use interactive OAuth2 PKCE login flow |
| `--state` | `spotcontrol_state.json` | Path to state file |
| `--devicename` | `EventWatcher` | Name shown in Spotify Connect device lists |
| `--port` | `0` | OAuth2 callback port (0 = random) |

## How It Works

After connecting, the example subscribes to all three event channels and enters a `select` loop that prints events as they arrive in real time:

```go
deviceCh := result.SubscribeDeviceList()
playbackCh := result.SubscribePlayback()
metadataCh := result.SubscribeMetadata()

for {
    select {
    case evt := <-deviceCh:
        // handle device list change
    case evt := <-playbackCh:
        // handle playback state change
    case evt := <-metadataCh:
        // handle track metadata change
    }
}
```

- **`SubscribeDeviceList()`** returns a `<-chan controller.DeviceListEvent` containing a snapshot of all devices, which devices triggered the update, and the reason.
- **`SubscribePlayback()`** returns a `<-chan controller.PlaybackEvent` containing a `PlayerState` with play/pause status, track URI, position, duration, shuffle, and repeat state.
- **`SubscribeMetadata()`** returns a `<-chan controller.MetadataEvent` containing a `TrackMetadata` with the track title, artist, album, duration, and cover art URL. Metadata is fetched automatically in the background from the private `GET /metadata/4/track/{hex_id}?market=from_token` endpoint whenever the playing track changes — no OAuth2 Web API token is needed.

All channels are buffered (16 deep) and are closed when the controller shuts down. If a subscriber falls behind, events are dropped rather than blocking the update loop.

## Example Output

```
Connecting to Spotify...
Connected as: username
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
  ...

^C
Shutting down...
Goodbye!
```
