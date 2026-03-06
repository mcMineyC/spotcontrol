package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	"github.com/mcMineyC/spotcontrol/quick"
)

const defaultStatePath = "spotcontrol_state.json"

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

func main() {
	deviceName := flag.String("devicename", "EventWatcher", "name of this device")
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

	fmt.Println("Connecting to Spotify...")

	result, err := quick.Connect(ctx, quick.QuickConfig{
		StatePath:    *statePath,
		DeviceName:   *deviceName,
		DeviceType:   spotcontrol.DeviceTypeComputer,
		Interactive:  *interactive,
		CallbackPort: *callbackPort,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer result.Close()

	fmt.Printf("Connected as: %s\n", result.Session.Username())
	fmt.Printf("Device ID:    %s\n", result.Session.DeviceId())
	fmt.Println()
	fmt.Println("Listening for events (Ctrl+C to quit)...")
	fmt.Println("---")

	// Subscribe to all three event channels.
	deviceCh := result.SubscribeDeviceList()
	playbackCh := result.SubscribePlayback()
	metadataCh := result.SubscribeMetadata()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Goodbye!")
			return

		case evt, ok := <-deviceCh:
			if !ok {
				fmt.Println("Device channel closed.")
				return
			}
			fmt.Printf("\n[DEVICES] Changed (reason=%s)\n", evt.Reason)
			if len(evt.DevicesThatChanged) > 0 {
				fmt.Printf("  Triggered by: %v\n", evt.DevicesThatChanged)
			}
			for _, d := range evt.Devices {
				active := ""
				if d.IsActive {
					active = " [ACTIVE]"
				}
				vol := ""
				if d.SupportsVolume {
					vol = fmt.Sprintf(" vol=%d%%", d.Volume)
				}
				fmt.Printf("  • %s (%s)%s%s\n", d.Name, d.Type, vol, active)
			}

		case evt, ok := <-playbackCh:
			if !ok {
				fmt.Println("Playback channel closed.")
				return
			}
			s := evt.State
			status := "⏸ Paused"
			if s.IsPlaying {
				status = "▶ Playing"
			}
			pos := formatDuration(s.PositionMs)
			dur := formatDuration(s.DurationMs)

			fmt.Printf("\n[PLAYBACK] %s\n", status)
			fmt.Printf("  Track:    %s\n", s.TrackURI)
			if s.ContextURI != "" {
				fmt.Printf("  Context:  %s\n", s.ContextURI)
			}
			fmt.Printf("  Position: %s / %s\n", pos, dur)
			if s.DeviceId != "" {
				fmt.Printf("  Device:   %s\n", s.DeviceId)
			}
			fmt.Printf("  Shuffle:  %v  Repeat: context=%v track=%v\n",
				s.Shuffle, s.RepeatContext, s.RepeatTrack)

		case evt, ok := <-metadataCh:
			if !ok {
				fmt.Println("Metadata channel closed.")
				return
			}
			m := evt.Metadata
			dur := formatDuration(m.DurationMs)

			fmt.Printf("\n[METADATA] 🎵 %s\n", m.Title)
			fmt.Printf("  Artist:   %s\n", m.Artist)
			fmt.Printf("  Album:    %s\n", m.Album)
			fmt.Printf("  Duration: %s\n", dur)
			fmt.Printf("  URI:      %s\n", m.TrackURI)
			if m.ImageURL != "" {
				fmt.Printf("  Cover:    %s\n", m.ImageURL)
			}
			if m.ArtistURI != "" {
				fmt.Printf("  Artist:   %s\n", m.ArtistURI)
			}
			if m.AlbumURI != "" {
				fmt.Printf("  Album:    %s\n", m.AlbumURI)
			}
		}
	}
}
