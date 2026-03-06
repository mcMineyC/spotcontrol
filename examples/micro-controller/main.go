package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	"github.com/mcMineyC/spotcontrol/controller"
	"github.com/mcMineyC/spotcontrol/quick"
)

const defaultStatePath = "spotcontrol_state.json"

func chooseDevice(ctrl *controller.Controller, reader *bufio.Reader) string {
	devices := ctrl.ListDevices()
	if len(devices) == 0 {
		fmt.Println("No devices available (cluster may not have been received yet).")
		fmt.Println("Try 'apidevices' to query the Web API directly.")
		return ""
	}

	fmt.Println("\nChoose a device:")
	for i, d := range devices {
		active := ""
		if d.IsActive {
			active = " [ACTIVE]"
		}
		fmt.Printf("  %d) %s (%s)%s\n", i, d.Name, d.Type, active)
	}

	for {
		fmt.Print("Enter device number: ")
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)
		i, err := strconv.Atoi(text)
		if err == nil && i >= 0 && i < len(devices) {
			return devices[i].Id
		}
		fmt.Println("Invalid device number.")
	}
}

// parseSpotifyURL parses a Spotify URL like
//
//	https://open.spotify.com/track/2AX9H0uIFZqo9zAcwclQy9?si=75167feac6a447db
//	https://open.spotify.com/playlist/5ese9XhQqKHoQg4WJ4sZef
//	https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy
//
// and returns the entity type ("track", "playlist", "album", etc.) and the
// base62 ID. Returns empty strings if the URL is not a recognised Spotify URL.
func parseSpotifyURL(raw string) (entityType string, id string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}

	// Accept open.spotify.com and play.spotify.com hosts.
	host := strings.ToLower(u.Hostname())
	if host != "open.spotify.com" && host != "play.spotify.com" {
		return "", ""
	}

	// Path is e.g. /track/2AX9H0uIFZqo9zAcwclQy9 or /intl-en/track/2AX9...
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")

	// Skip an optional locale segment like "intl-en".
	if len(parts) >= 3 && strings.HasPrefix(parts[0], "intl") {
		parts = parts[1:]
	}

	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}

	return parts[0], parts[1]
}

func printHelp() {
	fmt.Println("\nAvailable commands:")
	fmt.Println("  open <url>                  : play a Spotify URL (track, playlist, or album)")
	fmt.Println("  load <uri1> [uri2 ...]      : load track(s) via Web API (e.g. spotify:track:...)")
	fmt.Println("  playtrack <uri1> [uri2 ...]  : play track(s) via connect-state (no context/recommendations)")
	fmt.Println("  play                        : resume playback")
	fmt.Println("  pause                       : pause playback")
	fmt.Println("  next                        : skip to next track")
	fmt.Println("  prev                        : skip to previous track")
	fmt.Println("  volume <0-100>              : set volume percentage")
	fmt.Println("  seek <ms>                   : seek to position in milliseconds")
	fmt.Println("  shuffle <on|off>            : toggle shuffle mode")
	fmt.Println("  repeat <off|context|track>  : set repeat mode")
	fmt.Println("  playlist <id> [shuffle]     : play a playlist by ID (e.g. 5ese9XhQqKHoQg4WJ4sZef)")
	fmt.Println("  queue <uri>                 : add track to queue (connect-state)")
	fmt.Println("  transfer <device_id>        : transfer playback to device")
	fmt.Println("  devices                     : list devices from cluster state")
	fmt.Println("  apidevices                  : list devices from Web API")
	fmt.Println("  state                       : show current player state from Web API")
	fmt.Println("  metadata                    : show cached metadata for the current track")
	fmt.Println("  fetchmeta [uri]             : fetch metadata from private API (current track or given URI)")
	fmt.Println("  watch                       : toggle live event watching (devices/playback/metadata)")
	fmt.Println("  select                      : select a different target device")
	fmt.Println("  help                        : show this list")
	fmt.Println("  quit                        : exit")
	fmt.Println()
}

func main() {
	deviceName := flag.String("devicename", "SpotControl", "name of this device")
	interactive := flag.Bool("interactive", false, "use interactive OAuth2 PKCE login")
	callbackPort := flag.Int("port", 0, "OAuth2 callback port (0 = random)")
	statePath := flag.String("state", defaultStatePath, "path to state file")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	result, err := quick.Connect(ctx, quick.QuickConfig{
		StatePath:    *statePath,
		DeviceName:   *deviceName,
		DeviceType:   spotcontrol.DeviceTypeComputer,
		Interactive:  *interactive,
		CallbackPort: *callbackPort,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer result.Close()

	fmt.Printf("Connected as: %s\n", result.Session.Username())
	fmt.Printf("Device ID: %s\n", result.Session.DeviceId())

	reader := bufio.NewReader(os.Stdin)
	ident := ""
	ctrl := result.Controller

	// Subscribe to event channels for the "watch" command.
	deviceCh := ctrl.SubscribeDeviceList()
	playbackCh := ctrl.SubscribePlayback()
	metaCh := ctrl.SubscribeMetadata()

	var watching atomic.Bool

	// Background goroutine that prints events when watching is enabled.
	go func() {
		for {
			select {
			case evt, ok := <-deviceCh:
				if !ok {
					return
				}
				if watching.Load() {
					fmt.Printf("\n[EVENT] Device list changed (reason=%s, changed=%v):\n", evt.Reason, evt.DevicesThatChanged)
					for _, d := range evt.Devices {
						active := ""
						if d.IsActive {
							active = " [ACTIVE]"
						}
						fmt.Printf("  - %s (%s) id=%s vol=%d%%%s\n", d.Name, d.Type, d.Id, d.Volume, active)
					}
					fmt.Print(">>> ")
				}
			case evt, ok := <-playbackCh:
				if !ok {
					return
				}
				if watching.Load() {
					status := "paused"
					if evt.State.IsPlaying {
						status = "playing"
					}
					fmt.Printf("\n[EVENT] Playback: %s | %s | %dms/%dms | shuffle=%v repeat_ctx=%v repeat_trk=%v\n",
						status, evt.State.TrackURI, evt.State.PositionMs, evt.State.DurationMs,
						evt.State.Shuffle, evt.State.RepeatContext, evt.State.RepeatTrack)
					fmt.Print(">>> ")
				}
			case evt, ok := <-metaCh:
				if !ok {
					return
				}
				if watching.Load() {
					fmt.Printf("\n[EVENT] Metadata: %q by %q on %q (%dms)\n",
						evt.Metadata.Title, evt.Metadata.Artist, evt.Metadata.Album, evt.Metadata.DurationMs)
					if evt.Metadata.ImageURL != "" {
						fmt.Printf("        Art: %s\n", evt.Metadata.ImageURL)
					}
					fmt.Print(">>> ")
				}
			}
		}
	}()

	printHelp()

	for {
		fmt.Print(">>> ")
		text, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		cmds := strings.Fields(strings.TrimSpace(text))
		if len(cmds) == 0 {
			continue
		}

		switch cmds[0] {
		case "open":
			if len(cmds) < 2 {
				fmt.Println("Usage: open <spotify-url>")
				fmt.Println("  e.g. open https://open.spotify.com/track/2AX9H0uIFZqo9zAcwclQy9?si=abc123")
				fmt.Println("       open https://open.spotify.com/playlist/5ese9XhQqKHoQg4WJ4sZef")
				fmt.Println("       open https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy")
				continue
			}
			entityType, entityId := parseSpotifyURL(cmds[1])
			if entityType == "" || entityId == "" {
				fmt.Println("Could not parse Spotify URL. Expected format:")
				fmt.Println("  https://open.spotify.com/{track,playlist,album}/<id>")
				continue
			}

			switch entityType {
			case "track":
				trackURI := "spotify:track:" + entityId
				playOpts := &controller.PlayTrackOptions{}
				if ident != "" {
					playOpts.DeviceId = ident
				}
				if err := ctrl.PlayTrack(ctx, []string{trackURI}, playOpts); err != nil {
					fmt.Printf("Error: %v\n", err)
				} else {
					fmt.Printf("Playing track %s\n", entityId)
				}
			case "playlist":
				playOpts := &controller.PlayPlaylistOptions{
					DeviceId: ident,
				}
				if err := ctrl.PlayPlaylist(ctx, entityId, playOpts); err != nil {
					fmt.Printf("Error: %v\n", err)
				} else {
					fmt.Printf("Playing playlist %s\n", entityId)
				}
			case "album":
				// Albums are context-based, like playlists. Use LoadTrack
				// with a context_uri so the album plays in order.
				albumURI := "spotify:album:" + entityId
				opts := &controller.LoadTrackOptions{
					ContextURI: albumURI,
				}
				if ident != "" {
					opts.DeviceId = ident
				}
				if err := ctrl.LoadTrack(ctx, nil, opts); err != nil {
					fmt.Printf("Error: %v\n", err)
				} else {
					fmt.Printf("Playing album %s\n", entityId)
				}
			default:
				fmt.Printf("Unsupported Spotify entity type: %s\n", entityType)
				fmt.Println("Supported types: track, playlist, album")
			}

		case "playtrack":
			if len(cmds) < 2 {
				fmt.Println("Usage: playtrack <uri1> [uri2 ...]")
				continue
			}
			uris := cmds[1:]
			for i, uri := range uris {
				if !strings.HasPrefix(uri, "spotify:") {
					uris[i] = "spotify:track:" + uri
				}
			}
			playOpts := &controller.PlayTrackOptions{}
			if ident != "" {
				playOpts.DeviceId = ident
			}
			if err := ctrl.PlayTrack(ctx, uris, playOpts); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("Playing %d track(s) via connect-state...\n", len(uris))
			}

		case "playlist":
			if len(cmds) < 2 {
				fmt.Println("Usage: playlist <id> [shuffle]")
				continue
			}
			playlistId := cmds[1]
			// Strip spotify:playlist: prefix if provided.
			playlistId = strings.TrimPrefix(playlistId, "spotify:playlist:")
			shuffle := false
			if len(cmds) >= 3 && (cmds[2] == "shuffle" || cmds[2] == "true" || cmds[2] == "on") {
				shuffle = true
			}
			playOpts := &controller.PlayPlaylistOptions{
				DeviceId: ident,
				Shuffle:  shuffle,
			}
			if err := ctrl.PlayPlaylist(ctx, playlistId, playOpts); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("Playing playlist %s", playlistId)
				if shuffle {
					fmt.Print(" (shuffle on)")
				}
				fmt.Println()
			}

		case "play":
			if err := ctrl.Play(ctx, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Playing")
			}

		case "pause":
			if err := ctrl.Pause(ctx, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Paused")
			}

		case "next":
			if err := ctrl.Next(ctx, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Skipped to next")
			}

		case "prev":
			if err := ctrl.Previous(ctx, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Skipped to previous")
			}

		case "volume":
			if len(cmds) < 2 {
				fmt.Println("Usage: volume <0-100>")
				continue
			}
			vol, err := strconv.Atoi(cmds[1])
			if err != nil {
				fmt.Println("Invalid volume value")
				continue
			}
			if err := ctrl.SetVolume(ctx, vol, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("Volume set to %d%%\n", vol)
			}

		case "seek":
			if len(cmds) < 2 {
				fmt.Println("Usage: seek <milliseconds>")
				continue
			}
			pos, err := strconv.ParseInt(cmds[1], 10, 64)
			if err != nil {
				fmt.Println("Invalid position value")
				continue
			}
			if err := ctrl.Seek(ctx, pos, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("Seeked to %dms\n", pos)
			}

		case "shuffle":
			if len(cmds) < 2 {
				fmt.Println("Usage: shuffle <on|off>")
				continue
			}
			state := cmds[1] == "on" || cmds[1] == "true"
			if err := ctrl.SetShuffle(ctx, state, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("Shuffle: %v\n", state)
			}

		case "repeat":
			if len(cmds) < 2 {
				fmt.Println("Usage: repeat <off|context|track>")
				continue
			}
			if err := ctrl.SetRepeat(ctx, cmds[1], ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("Repeat: %s\n", cmds[1])
			}

		case "queue":
			if len(cmds) < 2 {
				fmt.Println("Usage: queue <uri>")
				continue
			}
			uri := cmds[1]
			if !strings.HasPrefix(uri, "spotify:") {
				uri = "spotify:track:" + uri
			}
			if err := ctrl.AddToQueue(ctx, uri, ident); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Added to queue")
			}

		case "transfer":
			if len(cmds) < 2 {
				fmt.Println("Usage: transfer <device_id>")
				continue
			}
			if err := ctrl.TransferPlayback(ctx, cmds[1], true); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Transferred playback")
			}

		case "devices":
			devices := ctrl.ListDevices()
			if len(devices) == 0 {
				fmt.Println("No devices in cluster (try 'apidevices' for Web API query)")
			} else {
				fmt.Println("\nDevices (from cluster):")
				for _, d := range devices {
					active := ""
					if d.IsActive {
						active = " [ACTIVE]"
					}
					fmt.Printf("  - %s (%s) id=%s vol=%d%%%s\n", d.Name, d.Type, d.Id, d.Volume, active)
				}
			}

		case "state":
			ps, err := ctrl.GetPlayerState(ctx)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else if ps == nil {
				fmt.Println("No active playback")
			} else {
				fmt.Printf("\nPlayer State:\n")
				fmt.Printf("  Playing:  %v\n", ps.IsPlaying)
				fmt.Printf("  Track:    %s\n", ps.TrackURI)
				fmt.Printf("  Context:  %s\n", ps.ContextURI)
				fmt.Printf("  Position: %dms / %dms\n", ps.PositionMs, ps.DurationMs)
				fmt.Printf("  Device:   %s\n", ps.DeviceId)
				fmt.Printf("  Shuffle:  %v\n", ps.Shuffle)
				fmt.Printf("  Repeat:   context=%v track=%v\n", ps.RepeatContext, ps.RepeatTrack)
			}

		case "metadata":
			meta := ctrl.GetTrackMetadata()
			if meta == nil {
				fmt.Println("No cached track metadata (no track playing or metadata not yet fetched)")
			} else {
				fmt.Printf("\nTrack Metadata (cached):\n")
				fmt.Printf("  Title:    %s\n", meta.Title)
				fmt.Printf("  Artist:   %s\n", meta.Artist)
				fmt.Printf("  Album:    %s\n", meta.Album)
				fmt.Printf("  Duration: %dms\n", meta.DurationMs)
				fmt.Printf("  URI:      %s\n", meta.TrackURI)
				if meta.ImageURL != "" {
					fmt.Printf("  Image:    %s\n", meta.ImageURL)
				}
				if meta.ArtistURI != "" {
					fmt.Printf("  ArtistURI: %s\n", meta.ArtistURI)
				}
				if meta.AlbumURI != "" {
					fmt.Printf("  AlbumURI:  %s\n", meta.AlbumURI)
				}
			}

		case "fetchmeta":
			var meta *controller.TrackMetadata
			var fetchErr error
			if len(cmds) >= 2 {
				uri := cmds[1]
				if !strings.HasPrefix(uri, "spotify:") {
					uri = "spotify:track:" + uri
				}
				meta, fetchErr = ctrl.FetchTrackMetadata(ctx, uri)
			} else {
				meta, fetchErr = ctrl.FetchCurrentTrackMetadata(ctx)
			}
			if fetchErr != nil {
				fmt.Printf("Error: %v\n", fetchErr)
			} else if meta == nil {
				fmt.Println("No track playing / no metadata available")
			} else {
				fmt.Printf("\nTrack Metadata (fetched):\n")
				fmt.Printf("  Title:    %s\n", meta.Title)
				fmt.Printf("  Artist:   %s\n", meta.Artist)
				fmt.Printf("  Album:    %s\n", meta.Album)
				fmt.Printf("  Duration: %dms\n", meta.DurationMs)
				fmt.Printf("  URI:      %s\n", meta.TrackURI)
				if meta.ImageURL != "" {
					fmt.Printf("  Image:    %s\n", meta.ImageURL)
				}
				if meta.SmallImageURL != "" {
					fmt.Printf("  SmallImg: %s\n", meta.SmallImageURL)
				}
			}

		case "watch":
			if watching.Load() {
				watching.Store(false)
				fmt.Println("Live event watching: OFF")
			} else {
				watching.Store(true)
				fmt.Println("Live event watching: ON (device/playback/metadata events will print as they arrive)")
			}

		case "select":
			ident = chooseDevice(ctrl, reader)
			if ident != "" {
				fmt.Printf("Selected device: %s\n", ident)
			}

		case "help":
			printHelp()

		case "quit", "exit", "q":
			fmt.Println("Goodbye!")
			return

		default:
			fmt.Printf("Unknown command: %s (type 'help' for available commands)\n", cmds[0])
		}
	}
}
