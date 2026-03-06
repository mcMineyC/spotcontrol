package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

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
// Sends a POST /v1/me/player/queue request through the spclient proxy.
func (c *Controller) AddToQueue(ctx context.Context, trackURI string, deviceId string) error {
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
