package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mcMineyC/spotcontrol/spclient"
)

// ---------------------------------------------------------------------------
// Playback control — connect-state protocol
// ---------------------------------------------------------------------------
//
// The methods below send playback commands through the connect-state player
// command endpoint:
//
//   POST /connect-state/v1/player/command/from/{fromDevice}/to/{toDevice}
//
// This is the same mechanism the Spotify desktop client uses to control remote
// devices. The body is a gzip-compressed JSON PlayerCommandRequest containing
// a command with an endpoint name (e.g. "resume", "pause", "skip_next") and
// optional parameters. The format was determined by capturing traffic from the
// Spotify desktop client via mitmproxy.
//
// When UseWebApi is set, commands still go through the public Web API at
// api.spotify.com (not recommended — subject to stricter rate limits).
//
// Volume uses a dedicated connect-state endpoint (same as librespot):
//   PUT /connect-state/v1/connect/volume/from/{fromDevice}/to/{toDevice}
//
// Playback transfer uses the connect-state transfer endpoint (same as librespot):
//   POST /connect-state/v1/connect/transfer/from/{fromDevice}/to/{toDevice}

// playerRequest sends a playback control request via the Web API player
// endpoints (/v1/me/player/*). By default, requests are routed through the
// spclient proxy — which goes through the spclient infrastructure and avoids
// the stricter public api.spotify.com rate limits. When UseWebApi is set,
// requests go directly to api.spotify.com instead.
func (c *Controller) playerRequest(ctx context.Context, method, path string, query url.Values, header http.Header, body []byte) (*http.Response, error) {
	if c.useWebApi {
		return c.sp.WebApiRequest(ctx, method, path, query, header, body)
	}
	return c.sp.Request(ctx, method, path, query, header, body)
}

// checkPlayerResponse validates common HTTP status codes from player commands
// and returns an error if the status code indicates failure.
func checkPlayerResponse(resp *http.Response) error {
	switch resp.StatusCode {
	case 200, 202, 204:
		return nil
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("command failed with status %d: %s", resp.StatusCode, string(body))
	}
}

// resolveTargetDevice determines the target device for a command. If deviceId
// is provided, it is used directly. Otherwise, the active device from the
// cluster state is used.
func (c *Controller) resolveTargetDevice(deviceId string) string {
	if deviceId != "" {
		return deviceId
	}
	return c.ActiveDeviceId()
}

// newLoggingParams creates a CommandLoggingParams matching the format observed
// from the Spotify desktop client. Includes command_initiated_time,
// command_received_time, device_identifier, command_id, and empty arrays for
// page_instance_ids and interaction_ids.
func (c *Controller) newLoggingParams() *spclient.CommandLoggingParams {
	now := time.Now().UnixMilli()
	received := now + 2 // desktop client shows ~2ms offset
	return &spclient.CommandLoggingParams{
		CommandInitiatedTime: &now,
		CommandReceivedTime:  &received,
		PageInstanceIds:      []string{},
		InteractionIds:       []string{},
		DeviceIdentifier:     c.deviceId,
		CommandId:            spclient.RandomHex(16),
	}
}

// newCommandOptions creates the default CommandOptions matching the Spotify
// desktop client (all false).
func (c *Controller) newCommandOptions() *spclient.CommandOptions {
	return &spclient.CommandOptions{
		OverrideRestrictions: false,
		OnlyForLocalDevice:   false,
		SystemInitiated:      false,
	}
}

// newCommandRequest wraps a PlayerCommand in a PlayerCommandRequest with
// the connection_type and intent_id fields matching the desktop client format.
func (c *Controller) newCommandRequest(cmd *spclient.PlayerCommand) *spclient.PlayerCommandRequest {
	return &spclient.PlayerCommandRequest{
		Command:        cmd,
		ConnectionType: "wlan",
		IntentId:       spclient.RandomHex(16),
	}
}

// sendPlayerCommand sends a connect-state player command to the specified
// target device. If the connect-state path fails or is unavailable, it falls
// back to the Web API proxy endpoint specified by fallbackMethod/fallbackPath.
func (c *Controller) sendPlayerCommand(
	ctx context.Context,
	targetDeviceId string,
	cmd *spclient.PlayerCommand,
	fallbackMethod string,
	fallbackPath string,
	fallbackQuery url.Values,
) error {
	// Try connect-state command endpoint first.
	connId := c.connectionId()
	target := c.resolveTargetDevice(targetDeviceId)

	if connId != "" && target != "" {
		cmdReq := c.newCommandRequest(cmd)

		err := c.sp.ConnectPlayerCommand(ctx, connId, target, cmdReq)
		if err == nil {
			return nil
		}
		c.log.Warnf("connect-state command %q failed, falling back to Web API: %v", cmd.Endpoint, err)
	}

	// Fall back to Web API proxy.
	if fallbackQuery == nil {
		fallbackQuery = url.Values{}
	}
	if targetDeviceId != "" {
		fallbackQuery.Set("device_id", targetDeviceId)
	}

	resp, err := c.playerRequest(ctx, fallbackMethod, fallbackPath, fallbackQuery, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending %s command: %w", cmd.Endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// Play resumes playback on the currently active device, or on the specified
// device if deviceId is non-empty.
//
// Sends a "resume" command through the connect-state player command endpoint.
// Falls back to PUT /v1/me/player/play if the connect-state path is
// unavailable.
func (c *Controller) Play(ctx context.Context, deviceId string) error {
	if c.useWebApi {
		return c.playWebApi(ctx, deviceId)
	}

	cmd := &spclient.PlayerCommand{
		Endpoint:      "resume",
		Options:       c.newCommandOptions(),
		LoggingParams: c.newLoggingParams(),
		ResumeOrigin:  &spclient.ResumeOrigin{FeatureIdentifier: "npb"},
	}

	return c.sendPlayerCommand(ctx, deviceId, cmd, "PUT", "/v1/me/player/play", nil)
}

func (c *Controller) playWebApi(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/play", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending play command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// Pause pauses playback on the currently active device, or on the specified
// device if deviceId is non-empty.
//
// Sends a "pause" command through the connect-state player command endpoint.
// Falls back to PUT /v1/me/player/pause if the connect-state path is
// unavailable.
func (c *Controller) Pause(ctx context.Context, deviceId string) error {
	if c.useWebApi {
		return c.pauseWebApi(ctx, deviceId)
	}

	cmd := &spclient.PlayerCommand{
		Endpoint:      "pause",
		Options:       c.newCommandOptions(),
		LoggingParams: c.newLoggingParams(),
	}

	return c.sendPlayerCommand(ctx, deviceId, cmd, "PUT", "/v1/me/player/pause", nil)
}

func (c *Controller) pauseWebApi(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/pause", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending pause command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// Next skips to the next track.
//
// Sends a "skip_next" command through the connect-state player command
// endpoint. Falls back to POST /v1/me/player/next if the connect-state path
// is unavailable.
func (c *Controller) Next(ctx context.Context, deviceId string) error {
	if c.useWebApi {
		return c.nextWebApi(ctx, deviceId)
	}

	cmd := &spclient.PlayerCommand{
		Endpoint:      "skip_next",
		Options:       c.newCommandOptions(),
		LoggingParams: c.newLoggingParams(),
	}

	return c.sendPlayerCommand(ctx, deviceId, cmd, "POST", "/v1/me/player/next", nil)
}

func (c *Controller) nextWebApi(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "POST", "/v1/me/player/next", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending next command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// Previous skips to the previous track.
//
// Sends a "skip_prev" command through the connect-state player command
// endpoint. The target device handles seek-to-zero vs skip-to-previous logic.
// Falls back to POST /v1/me/player/previous if the connect-state path is
// unavailable.
func (c *Controller) Previous(ctx context.Context, deviceId string) error {
	if c.useWebApi {
		return c.previousWebApi(ctx, deviceId)
	}

	opts := c.newCommandOptions()
	opts.AllowSeeking = true
	cmd := &spclient.PlayerCommand{
		Endpoint:      "skip_prev",
		Options:       opts,
		LoggingParams: c.newLoggingParams(),
	}

	return c.sendPlayerCommand(ctx, deviceId, cmd, "POST", "/v1/me/player/previous", nil)
}

func (c *Controller) previousWebApi(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "POST", "/v1/me/player/previous", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending previous command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// Seek seeks to the given position in the current track.
//
// Sends a "seek_to" command through the connect-state player command endpoint.
// Falls back to PUT /v1/me/player/seek if the connect-state path is
// unavailable.
func (c *Controller) Seek(ctx context.Context, positionMs int64, deviceId string) error {
	if c.useWebApi {
		return c.seekWebApi(ctx, positionMs, deviceId)
	}

	// The connect-state seek_to command uses "value" for absolute position
	// (matching go-librespot and librespot-rs handling of the seek_to endpoint).
	cmd := &spclient.PlayerCommand{
		Endpoint:      "seek_to",
		Value:         positionMs,
		Options:       c.newCommandOptions(),
		LoggingParams: c.newLoggingParams(),
	}

	return c.sendPlayerCommand(ctx, deviceId, cmd, "PUT", "/v1/me/player/seek",
		url.Values{"position_ms": []string{fmt.Sprintf("%d", positionMs)}})
}

func (c *Controller) seekWebApi(ctx context.Context, positionMs int64, deviceId string) error {
	query := url.Values{
		"position_ms": []string{fmt.Sprintf("%d", positionMs)},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/seek", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending seek command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// SetShuffle enables or disables shuffle mode.
//
// Sends a "set_shuffling_context" command through the connect-state player
// command endpoint. Falls back to PUT /v1/me/player/shuffle if the
// connect-state path is unavailable.
func (c *Controller) SetShuffle(ctx context.Context, state bool, deviceId string) error {
	if c.useWebApi {
		return c.setShuffleWebApi(ctx, state, deviceId)
	}

	cmd := &spclient.PlayerCommand{
		Endpoint:      "set_shuffling_context",
		Value:         state,
		Options:       c.newCommandOptions(),
		LoggingParams: c.newLoggingParams(),
	}

	return c.sendPlayerCommand(ctx, deviceId, cmd, "PUT", "/v1/me/player/shuffle",
		url.Values{"state": []string{fmt.Sprintf("%t", state)}})
}

func (c *Controller) setShuffleWebApi(ctx context.Context, state bool, deviceId string) error {
	query := url.Values{
		"state": []string{fmt.Sprintf("%t", state)},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/shuffle", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending shuffle command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}

// SetRepeat sets the repeat mode. Valid values are "off", "context", and "track".
//
// Sends "set_repeating_context" and/or "set_repeating_track" commands through
// the connect-state player command endpoint. Falls back to
// PUT /v1/me/player/repeat if the connect-state path is unavailable.
func (c *Controller) SetRepeat(ctx context.Context, state string, deviceId string) error {
	if c.useWebApi {
		return c.setRepeatWebApi(ctx, state, deviceId)
	}

	// The connect-state protocol uses two separate boolean commands for repeat,
	// unlike the Web API which uses a single "off"/"context"/"track" string.
	connId := c.connectionId()
	target := c.resolveTargetDevice(deviceId)

	if connId != "" && target != "" {
		var repeatContext, repeatTrack bool
		switch state {
		case "context":
			repeatContext = true
			repeatTrack = false
		case "track":
			repeatContext = true
			repeatTrack = true
		default: // "off"
			repeatContext = false
			repeatTrack = false
		}

		// Send set_repeating_context command.
		ctxCmd := c.newCommandRequest(&spclient.PlayerCommand{
			Endpoint:      "set_repeating_context",
			Value:         repeatContext,
			Options:       c.newCommandOptions(),
			LoggingParams: c.newLoggingParams(),
		})
		if err := c.sp.ConnectPlayerCommand(ctx, connId, target, ctxCmd); err != nil {
			c.log.Warnf("connect-state set_repeating_context failed, falling back to Web API: %v", err)
			return c.setRepeatWebApi(ctx, state, deviceId)
		}

		// Send set_repeating_track command.
		trackCmd := c.newCommandRequest(&spclient.PlayerCommand{
			Endpoint:      "set_repeating_track",
			Value:         repeatTrack,
			Options:       c.newCommandOptions(),
			LoggingParams: c.newLoggingParams(),
		})
		if err := c.sp.ConnectPlayerCommand(ctx, connId, target, trackCmd); err != nil {
			c.log.Warnf("connect-state set_repeating_track failed: %v", err)
			return err
		}

		return nil
	}

	// Fall back to Web API.
	return c.setRepeatWebApi(ctx, state, deviceId)
}

func (c *Controller) setRepeatWebApi(ctx context.Context, state string, deviceId string) error {
	query := url.Values{
		"state": []string{state},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/repeat", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending repeat command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}
