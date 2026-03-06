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

// ---------------------------------------------------------------------------
// Track loading and playback transfer
// ---------------------------------------------------------------------------

// LoadTrackOptions configures the LoadTrack command.
type LoadTrackOptions struct {
	// DeviceId is the target device. If empty, the currently active device is used.
	DeviceId string
	// ContextURI is an optional context (album, playlist, artist) URI. If set,
	// the tracks are played within this context.
	ContextURI string
	// OffsetURI is the URI of the track to start playback from within the
	// context. Only used when ContextURI is set.
	OffsetURI string
	// OffsetPosition is the zero-based index within the context to start
	// playback from. Only used when ContextURI is set and OffsetURI is empty.
	OffsetPosition *int
	// PositionMs is the position within the track to start playback from.
	PositionMs int64
}

// LoadTrack starts playback of the given track URIs (e.g.
// "spotify:track:6rqhFgbbKwnb9MLmUQDhG6") on the specified device.
//
// Sends a PUT /v1/me/player/play request through the spclient proxy with a
// JSON body containing the track URIs, context, and offset options.
func (c *Controller) LoadTrack(ctx context.Context, trackURIs []string, opts *LoadTrackOptions) error {
	if opts == nil {
		opts = &LoadTrackOptions{}
	}

	query := url.Values{}
	if opts.DeviceId != "" {
		query.Set("device_id", opts.DeviceId)
	}

	body := map[string]interface{}{}

	if opts.ContextURI != "" {
		body["context_uri"] = opts.ContextURI
	}

	if len(trackURIs) > 0 && opts.ContextURI == "" {
		body["uris"] = trackURIs
	}

	if opts.OffsetURI != "" {
		body["offset"] = map[string]interface{}{"uri": opts.OffsetURI}
	} else if opts.OffsetPosition != nil {
		body["offset"] = map[string]interface{}{"position": *opts.OffsetPosition}
	}

	if opts.PositionMs > 0 {
		body["position_ms"] = opts.PositionMs
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed marshalling load track body: %w", err)
	}

	header := http.Header{
		"Content-Type": []string{"application/json"},
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/play", query, header, bodyBytes)
	if err != nil {
		return fmt.Errorf("failed sending load track command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// TransferPlayback transfers playback to the specified device. If play is true,
// playback starts immediately on the target device; otherwise the device becomes
// active but remains paused.
//
// When using the connect-state protocol, this uses the dedicated transfer
// endpoint (POST /connect-state/v1/connect/transfer/from/{from}/to/{to}),
// which is the same endpoint librespot uses (see spclient.rs transfer()).
func (c *Controller) TransferPlayback(ctx context.Context, deviceId string, play bool) error {
	// Try the connect-state transfer endpoint first (same as librespot).
	connId := c.connectionId()
	if connId != "" {
		// Determine the source device. Use the active device from cluster if
		// available, otherwise use our own device ID (which tells the backend
		// to transfer from whoever is currently active).
		fromDevice := c.ActiveDeviceId()
		if fromDevice == "" {
			fromDevice = c.deviceId
		}

		// Build the transfer request matching librespot's TransferRequest struct.
		var transferReq *spclient.TransferRequest
		if !play {
			restore := "restore"
			transferReq = &spclient.TransferRequest{
				TransferOptions: spclient.TransferOptions{
					RestorePaused: &restore,
				},
			}
		}

		err := c.sp.ConnectTransfer(ctx, connId, fromDevice, deviceId, transferReq)
		if err == nil {
			return nil
		}
		c.log.Warnf("connect-state transfer failed, falling back to Web API: %v", err)
	}

	// Fall back to the Web API transfer endpoint.
	return c.transferPlaybackWebApi(ctx, deviceId, play)
}

func (c *Controller) transferPlaybackWebApi(ctx context.Context, deviceId string, play bool) error {
	body := map[string]interface{}{
		"device_ids": []string{deviceId},
		"play":       play,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed marshalling transfer playback body: %w", err)
	}

	header := http.Header{
		"Content-Type": []string{"application/json"},
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player", nil, header, bodyBytes)
	if err != nil {
		return fmt.Errorf("failed sending transfer playback command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// AddToQueue adds a track to the user's playback queue.
//
// Sends an "add_to_queue" command through the connect-state player command
// endpoint, matching the format captured from the Spotify desktop client
// (cuts/queue.bin). The command body contains the track URI in a track object
// with an empty uid and empty metadata map.
//
// Falls back to POST /v1/me/player/queue if the connect-state path is
// unavailable.
func (c *Controller) AddToQueue(ctx context.Context, trackURI string, deviceId string) error {
	if c.useWebApi {
		return c.addToQueueWebApi(ctx, trackURI, deviceId)
	}

	cmd := &spclient.PlayerCommand{
		Endpoint: "add_to_queue",
		Track: &connectpb.ContextTrack{
			Uri:      trackURI,
			Uid:      "",
			Metadata: map[string]string{},
		},
		Options:       c.newCommandOptions(),
		LoggingParams: c.newLoggingParams(),
	}

	return c.sendPlayerCommand(ctx, deviceId, cmd, "POST", "/v1/me/player/queue",
		url.Values{"uri": []string{trackURI}})
}

func (c *Controller) addToQueueWebApi(ctx context.Context, trackURI string, deviceId string) error {
	query := url.Values{
		"uri": []string{trackURI},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "POST", "/v1/me/player/queue", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending add to queue command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// PlayTrackOptions configures the PlayTrack command.
type PlayTrackOptions struct {
	// DeviceId is the target device. If empty, the currently active device is used.
	DeviceId string

	// Shuffle enables shuffle mode. Only meaningful when multiple track URIs
	// are provided. Default false.
	Shuffle bool

	// SkipToURI starts playback from this specific track URI within the
	// provided list. If empty, playback starts from the first track.
	SkipToURI string

	// SkipToIndex starts playback from this zero-based index within the
	// provided list. Only used if SkipToURI is empty.
	SkipToIndex *int
}

// PlayTrack starts playback of one or more tracks by their Spotify URIs using
// the connect-state player command endpoint. Unlike PlayPlaylist, this does
// not require a playlist/album context — the tracks are played directly,
// without context-based recommendations or radio.
//
// The command format matches the "play" endpoint captured from the Spotify
// desktop client (cuts/load.bin), but uses the track URI as the context URI
// and embeds the tracks directly in a single context page. This tells the
// player to play exactly these tracks with no surrounding context.
//
// Falls back to the Web API PUT /v1/me/player/play endpoint if the
// connect-state path is unavailable.
func (c *Controller) PlayTrack(ctx context.Context, trackURIs []string, opts *PlayTrackOptions) error {
	if len(trackURIs) == 0 {
		return fmt.Errorf("at least one track URI is required")
	}
	if opts == nil {
		opts = &PlayTrackOptions{}
	}

	// Build context tracks for the page.
	contextTracks := make([]*connectpb.ContextTrack, len(trackURIs))
	for i, uri := range trackURIs {
		contextTracks[i] = &connectpb.ContextTrack{
			Uri:      uri,
			Uid:      spclient.RandomHex(8),
			Metadata: map[string]string{},
		}
	}

	// Use the first track URI as the context URI when playing bare tracks.
	contextURI := trackURIs[0]
	contextURL := "context://" + contextURI

	// Build the context with an inline page containing all tracks.
	playContext := &connectpb.Context{
		Uri: contextURI,
		Url: contextURL,
		Pages: []*connectpb.ContextPage{
			{
				Tracks:   contextTracks,
				Metadata: map[string]string{},
			},
		},
	}

	// Build the play origin — "your_library" with feature "track".
	playOrigin := &connectpb.PlayOrigin{
		FeatureIdentifier:  "track",
		FeatureVersion:     "spotcontrol/" + spclient.RandomHex(4),
		FeatureClasses:     []string{},
		ReferrerIdentifier: "your_library",
	}

	// Build skip_to targeting the desired track.
	var skipTo *spclient.CommandSkipTo
	if opts.SkipToURI != "" {
		// Find the matching UID.
		var uid string
		for _, ct := range contextTracks {
			if ct.Uri == opts.SkipToURI {
				uid = ct.Uid
				break
			}
		}
		skipTo = &spclient.CommandSkipTo{
			TrackUri: opts.SkipToURI,
			TrackUid: uid,
		}
	} else if opts.SkipToIndex != nil && *opts.SkipToIndex >= 0 && *opts.SkipToIndex < len(contextTracks) {
		idx := *opts.SkipToIndex
		skipTo = &spclient.CommandSkipTo{
			TrackUri:   contextTracks[idx].Uri,
			TrackUid:   contextTracks[idx].Uid,
			TrackIndex: idx,
		}
	}

	// Build prepare_play_options matching the captured load.bin format.
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
		c.log.Warnf("connect-state play track failed, falling back to Web API: %v", err)
	}

	// Fall back to Web API PUT /v1/me/player/play with uris body.
	return c.playTrackWebApi(ctx, trackURIs, opts)
}

// playTrackWebApi falls back to the Web API for track playback.
func (c *Controller) playTrackWebApi(ctx context.Context, trackURIs []string, opts *PlayTrackOptions) error {
	query := url.Values{}
	if opts.DeviceId != "" {
		query.Set("device_id", opts.DeviceId)
	}

	body := map[string]interface{}{
		"uris": trackURIs,
	}

	if opts.SkipToURI != "" {
		body["offset"] = map[string]interface{}{"uri": opts.SkipToURI}
	} else if opts.SkipToIndex != nil {
		body["offset"] = map[string]interface{}{"position": *opts.SkipToIndex}
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed marshalling play track body: %w", err)
	}

	header := http.Header{
		"Content-Type": []string{"application/json"},
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/play", query, header, bodyBytes)
	if err != nil {
		return fmt.Errorf("failed sending play track command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}
