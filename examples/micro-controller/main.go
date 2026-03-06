package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	spotcontrol "github.com/badfortrains/spotcontrol"
	"github.com/badfortrains/spotcontrol/controller"
	devicespb "github.com/badfortrains/spotcontrol/proto/spotify/connectstate/devices"
	"github.com/badfortrains/spotcontrol/session"
)

const (
	defaultDeviceName = "SpotControl"
	stateFileName     = "spotcontrol_state.json"
)

// simpleLogger implements spotcontrol.Logger with fmt output.
type simpleLogger struct {
	prefix string
}

func (l *simpleLogger) Tracef(format string, args ...interface{}) {}
func (l *simpleLogger) Debugf(format string, args ...interface{}) {
	fmt.Printf("[DBG] "+format+"\n", args...)
}
func (l *simpleLogger) Infof(format string, args ...interface{}) {
	fmt.Printf("[INF] "+format+"\n", args...)
}
func (l *simpleLogger) Warnf(format string, args ...interface{}) {
	fmt.Printf("[WRN] "+format+"\n", args...)
}
func (l *simpleLogger) Errorf(format string, args ...interface{}) {
	fmt.Printf("[ERR] "+format+"\n", args...)
}

func (l *simpleLogger) Trace(args ...interface{}) {}
func (l *simpleLogger) Debug(args ...interface{}) {
	fmt.Println(append([]interface{}{"[DBG]"}, args...)...)
}
func (l *simpleLogger) Info(args ...interface{}) {
	fmt.Println(append([]interface{}{"[INF]"}, args...)...)
}
func (l *simpleLogger) Warn(args ...interface{}) {
	fmt.Println(append([]interface{}{"[WRN]"}, args...)...)
}
func (l *simpleLogger) Error(args ...interface{}) {
	fmt.Println(append([]interface{}{"[ERR]"}, args...)...)
}

func (l *simpleLogger) WithField(key string, value interface{}) spotcontrol.Logger {
	return &simpleLogger{prefix: fmt.Sprintf("%s[%s=%v]", l.prefix, key, value)}
}
func (l *simpleLogger) WithError(err error) spotcontrol.Logger {
	return &simpleLogger{prefix: fmt.Sprintf("%s[err=%v]", l.prefix, err)}
}

func generateDeviceId() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed generating device id: %v", err))
	}
	return hex.EncodeToString(b)
}

func loadState(path string) *spotcontrol.AppState {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state spotcontrol.AppState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}

func saveState(path string, state *spotcontrol.AppState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

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

func getDevice(ctrl *controller.Controller, ident string, reader *bufio.Reader) string {
	if ident != "" {
		return ident
	}
	return chooseDevice(ctrl, reader)
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
	username := flag.String("username", "", "spotify username (for stored credentials)")
	storedCredPath := flag.String("credentials", "", "path to stored credentials JSON file")
	deviceName := flag.String("devicename", defaultDeviceName, "name of this device")
	interactive := flag.Bool("interactive", false, "use interactive OAuth2 PKCE login")
	callbackPort := flag.Int("port", 0, "OAuth2 callback port (0 = random)")
	flag.Parse()

	log := &simpleLogger{}
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

	// Determine credentials.
	var creds any
	var state *spotcontrol.AppState

	// Try loading saved state first.
	if *storedCredPath != "" {
		state = loadState(*storedCredPath)
	} else {
		state = loadState(stateFileName)
	}

	if state != nil && len(state.StoredCredentials) > 0 && state.Username != "" {
		log.Infof("using stored credentials for user %s", state.Username)
		creds = session.StoredCredentials{
			Username: state.Username,
			Data:     state.StoredCredentials,
		}
	} else if *interactive {
		creds = session.InteractiveCredentials{
			CallbackPort: *callbackPort,
		}
	} else if *username != "" {
		// Prompt for password.
		fmt.Print("Password: ")
		reader := bufio.NewReader(os.Stdin)
		password, _ := reader.ReadString('\n')
		password = strings.TrimSpace(password)
		// Use interactive token flow instead since password login is deprecated.
		// For backwards compatibility we'll try interactive if no stored creds.
		fmt.Println("Note: password-based login is no longer supported.")
		fmt.Println("Please use --interactive flag for OAuth2 PKCE login.")
		os.Exit(1)
	} else {
		fmt.Println("Usage:")
		fmt.Println("  First time (interactive OAuth2 login):")
		fmt.Println("    ./micro-controller --interactive")
		fmt.Println()
		fmt.Println("  Subsequent runs (uses saved credentials):")
		fmt.Println("    ./micro-controller")
		fmt.Println()
		fmt.Println("  With custom credentials file:")
		fmt.Println("    ./micro-controller --credentials path/to/state.json")
		fmt.Println()
		fmt.Println("Flags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Determine device ID.
	deviceId := ""
	if state != nil && state.DeviceId != "" {
		deviceId = state.DeviceId
	} else {
		deviceId = generateDeviceId()
	}

	// Create session.
	log.Infof("connecting to Spotify...")
	sess, err := session.NewSessionFromOptions(ctx, &session.Options{
		Log:         log,
		DeviceType:  devicespb.DeviceType_COMPUTER,
		DeviceId:    deviceId,
		DeviceName:  *deviceName,
		Credentials: creds,
	})
	if err != nil {
		fmt.Printf("Error creating session: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	// Save credentials for next time.
	savePath := stateFileName
	if *storedCredPath != "" {
		savePath = *storedCredPath
	}
	newState := &spotcontrol.AppState{
		DeviceId:          deviceId,
		Username:          sess.Username(),
		StoredCredentials: sess.StoredCredentials(),
	}
	if err := saveState(savePath, newState); err != nil {
		log.Warnf("failed saving state: %v", err)
	} else {
		log.Infof("saved credentials to %s", savePath)
	}

	// Create controller.
	ctrl := controller.NewController(controller.Config{
		Log:        log,
		Spclient:   sess.Spclient(),
		Dealer:     sess.Dealer(),
		DeviceId:   deviceId,
		DeviceName: *deviceName,
		DeviceType: devicespb.DeviceType_COMPUTER,
	})
	defer ctrl.Close()

	// Start the controller (connects dealer, subscribes to cluster updates).
	if err := ctrl.Start(ctx); err != nil {
		fmt.Printf("Error starting controller: %v\n", err)
		os.Exit(1)
	}

	log.Infof("session established for user: %s", sess.Username())
	log.Infof("device ID: %s", deviceId)

	reader := bufio.NewReader(os.Stdin)
	ident := ""

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
					fmt.Printf("  - %s (%s) id=%s vol=%d%s\n", d.Name, d.Type, d.Id, d.Volume, active)
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
