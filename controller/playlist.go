package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	connectpb "github.com/mcMineyC/spotcontrol/proto/spotify/connectstate"
	"github.com/mcMineyC/spotcontrol/spclient"
)

// PlayPlaylistOptions configures the PlayPlaylist command.
type PlayPlaylistOptions struct {
	// DeviceId is the target device. If empty, the currently active device is used.
	DeviceId string

	// Shuffle enables shuffle mode for the playlist. Default false.
	Shuffle bool

	// SkipToTrackURI starts playback from this specific track URI within the
	// playlist. If empty, playback starts from the beginning (or a random
	// track if Shuffle is true).
	SkipToTrackURI string

	// SkipToTrackUID starts playback from the track with this UID within the
	// playlist context. Only used if SkipToTrackURI is empty.
	SkipToTrackUID string
}

// PlayPlaylist starts playback of a Spotify playlist by its ID (the 22-character
// base62 identifier, e.g. "5ese9XhQqKHoQg4WJ4sZef"). The command format
// matches the Spotify desktop client's playlist play command as captured in
// cuts/playlist.bin.
//
// The playlist ID is the only required parameter. All context setup (URI
// construction, play origin, prepare options, play options, logging params) is
// handled automatically, matching the format the Spotify desktop client sends
// to the connect-state player command endpoint.
//
// This sends a "play" command through the connect-state player command
// endpoint. If the connect-state path is unavailable, it falls back to the
// Web API PUT /v1/me/player/play endpoint with a context_uri body.
func (c *Controller) PlayPlaylist(ctx context.Context, playlistId string, opts *PlayPlaylistOptions) error {
	if opts == nil {
		opts = &PlayPlaylistOptions{}
	}

	playlistURI := "spotify:playlist:" + playlistId
	contextURL := "context://" + playlistURI

	// Build the context matching the captured playlist.bin format.
	playContext := &connectpb.Context{
		Uri: playlistURI,
		Url: contextURL,
	}

	// Build the play origin matching the captured format.
	playOrigin := &connectpb.PlayOrigin{
		FeatureIdentifier:  "playlist",
		FeatureVersion:     "spotcontrol/" + spclient.RandomHex(4),
		FeatureClasses:     []string{},
		ReferrerIdentifier: "your_library",
	}

	// Build skip_to if a specific track is requested.
	var skipTo *spclient.CommandSkipTo
	if opts.SkipToTrackURI != "" || opts.SkipToTrackUID != "" {
		skipTo = &spclient.CommandSkipTo{
			TrackUri: opts.SkipToTrackURI,
			TrackUid: opts.SkipToTrackUID,
		}
	}

	// Build prepare_play_options matching the captured format.
	preparePlayOpts := &spclient.PreparePlayOptions{
		AlwaysPlaySomething: false,
		SkipTo:              skipTo,
		InitiallyPaused:     false,
		SystemInitiated:     false,
		PlayerOptionsOverride: &spclient.PlayerOptionsOverride{
			ShufflingContext: opts.Shuffle,
			Modes: &spclient.PlayerOptionsOverrideModes{
				ContextEnhancement: "NONE",
			},
		},
		SessionId:             spclient.RandomHex(16),
		License:               "premium",
		Suppressions:          &connectpb.Suppressions{},
		PrefetchLevel:         "none",
		AudioStream:           "default",
		ConfigurationOverride: &spclient.ConfigurationOverride{},
	}

	// Build play_options matching the captured format.
	playOpts := &spclient.PlayOptions{
		Reason:               "interactive",
		Operation:            "replace",
		Trigger:              "immediately",
		OverrideRestrictions: false,
		OnlyForLocalDevice:   false,
		SystemInitiated:      false,
	}

	cmd := &spclient.PlayerCommand{
		Endpoint:           "play",
		Context:            playContext,
		PlayOrigin:         playOrigin,
		PreparePlayOptions: preparePlayOpts,
		PlayOptions:        playOpts,
		LoggingParams:      c.newLoggingParams(),
	}

	// Try connect-state command endpoint first.
	connId := c.connectionId()
	target := c.resolveTargetDevice(opts.DeviceId)

	if connId != "" && target != "" {
		cmdReq := c.newCommandRequest(cmd)

		err := c.sp.ConnectPlayerCommand(ctx, connId, target, cmdReq)
		if err == nil {
			return nil
		}
		c.log.Warnf("connect-state play playlist failed, falling back to Web API: %v", err)
	}

	// Fall back to Web API PUT /v1/me/player/play with context_uri.
	return c.playPlaylistWebApi(ctx, playlistURI, opts)
}

// playPlaylistWebApi falls back to the Web API for playlist playback.
func (c *Controller) playPlaylistWebApi(ctx context.Context, playlistURI string, opts *PlayPlaylistOptions) error {
	query := url.Values{}
	if opts.DeviceId != "" {
		query.Set("device_id", opts.DeviceId)
	}

	body := map[string]interface{}{
		"context_uri": playlistURI,
	}

	if opts.SkipToTrackURI != "" {
		body["offset"] = map[string]interface{}{"uri": opts.SkipToTrackURI}
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed marshalling play playlist body: %w", err)
	}

	header := http.Header{
		"Content-Type": []string{"application/json"},
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/play", query, header, bodyBytes)
	if err != nil {
		return fmt.Errorf("failed sending play playlist command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}
