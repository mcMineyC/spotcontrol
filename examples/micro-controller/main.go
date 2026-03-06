package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
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

func printHelp() {
	fmt.Println("\nAvailable commands:")
	fmt.Println("  load <uri1> [uri2 ...] : load track(s) by Spotify URI (e.g. spotify:track:...)")
	fmt.Println("  play                   : resume playback")
	fmt.Println("  pause                  : pause playback")
	fmt.Println("  next                   : skip to next track")
	fmt.Println("  prev                   : skip to previous track")
	fmt.Println("  volume <0-100>         : set volume percentage")
	fmt.Println("  seek <ms>              : seek to position in milliseconds")
	fmt.Println("  shuffle <on|off>       : toggle shuffle mode")
	fmt.Println("  repeat <off|context|track> : set repeat mode")
	fmt.Println("  queue <uri>            : add track to queue")
	fmt.Println("  transfer <device_id>   : transfer playback to device")
	fmt.Println("  devices                : list devices from cluster state")
	fmt.Println("  apidevices             : list devices from Web API")
	fmt.Println("  state                  : show current player state from Web API")
	fmt.Println("  select                 : select a different target device")
	fmt.Println("  help                   : show this list")
	fmt.Println("  quit                   : exit")
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
		case "load":
			if len(cmds) < 2 {
				fmt.Println("Usage: load <uri1> [uri2 ...]")
				continue
			}
			uris := cmds[1:]
			// Ensure URIs have the spotify: prefix.
			for i, uri := range uris {
				if !strings.HasPrefix(uri, "spotify:") {
					uris[i] = "spotify:track:" + uri
				}
			}
			opts := &controller.LoadTrackOptions{}
			if ident != "" {
				opts.DeviceId = ident
			}
			if err := ctrl.LoadTrack(ctx, uris, opts); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Loading tracks...")
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

		case "apidevices":
			devices, err := ctrl.ListDevicesFromAPI(ctx)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else if len(devices) == 0 {
				fmt.Println("No devices found")
			} else {
				fmt.Println("\nDevices (from Web API):")
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
