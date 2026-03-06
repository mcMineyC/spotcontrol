package spclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/protobuf/proto"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	connectpb "github.com/mcMineyC/spotcontrol/proto/spotify/connectstate"
)

// ---------------------------------------------------------------------------
// Connect-state player command types
// ---------------------------------------------------------------------------

// CommandLoggingParams mirrors the logging_params object sent with
// connect-state player commands. All fields are optional.
type CommandLoggingParams struct {
	CommandInitiatedTime *int64   `json:"command_initiated_time,omitempty"`
	CommandReceivedTime  *int64   `json:"command_received_time,omitempty"`
	PageInstanceIds      []string `json:"page_instance_ids"`
	InteractionIds       []string `json:"interaction_ids"`
	DeviceIdentifier     string   `json:"device_identifier,omitempty"`
	CommandId            string   `json:"command_id,omitempty"`
}

// CommandSkipTo specifies which track to skip to within a play command.
type CommandSkipTo struct {
	TrackUid   string `json:"track_uid,omitempty"`
	TrackUri   string `json:"track_uri,omitempty"`
	TrackIndex int    `json:"track_index,omitempty"`
}

// CommandOptions contains options sent with connect-state player commands.
// The Spotify desktop client sends these with every command.
type CommandOptions struct {
	RestorePaused        string         `json:"restore_paused,omitempty"`
	RestorePosition      string         `json:"restore_position,omitempty"`
	RestoreTrack         string         `json:"restore_track,omitempty"`
	AllowSeeking         bool           `json:"allow_seeking,omitempty"`
	OverrideRestrictions bool           `json:"override_restrictions"`
	OnlyForLocalDevice   bool           `json:"only_for_local_device"`
	SystemInitiated      bool           `json:"system_initiated"`
	SkipTo               *CommandSkipTo `json:"skip_to,omitempty"`
}

// ResumeOrigin is sent with "resume" commands by the Spotify desktop client.
type ResumeOrigin struct {
	FeatureIdentifier string `json:"feature_identifier,omitempty"`
}

// PreparePlayOptions carries the prepare_play_options object sent with "play"
// commands. This was captured from the Spotify desktop client's playlist play
// command (cuts/playlist.bin). It controls shuffle, skip-to, session, license,
// and other playback preparation parameters.
type PreparePlayOptions struct {
	AlwaysPlaySomething   bool                    `json:"always_play_something"`
	SkipTo                *CommandSkipTo          `json:"skip_to,omitempty"`
	InitiallyPaused       bool                    `json:"initially_paused"`
	SystemInitiated       bool                    `json:"system_initiated"`
	PlayerOptionsOverride *PlayerOptionsOverride  `json:"player_options_override,omitempty"`
	SessionId             string                  `json:"session_id,omitempty"`
	License               string                  `json:"license,omitempty"`
	Suppressions          *connectpb.Suppressions `json:"suppressions,omitempty"`
	PrefetchLevel         string                  `json:"prefetch_level,omitempty"`
	AudioStream           string                  `json:"audio_stream,omitempty"`
	ConfigurationOverride *ConfigurationOverride  `json:"configuration_override,omitempty"`
}

// PlayerOptionsOverride carries player option overrides for the
// prepare_play_options. The Spotify desktop client sends this to control
// shuffle state and context enhancement mode when starting playlist playback.
type PlayerOptionsOverride struct {
	ShufflingContext bool                        `json:"shuffling_context"`
	Modes            *PlayerOptionsOverrideModes `json:"modes,omitempty"`
}

// PlayerOptionsOverrideModes carries mode overrides within PlayerOptionsOverride.
type PlayerOptionsOverrideModes struct {
	ContextEnhancement string `json:"context_enhancement,omitempty"`
}

// ConfigurationOverride is a placeholder for the configuration_override object
// sent with "play" commands. Currently observed as empty ({}) from the desktop
// client.
type ConfigurationOverride struct{}

// PlayOptions carries the play_options object sent with "play" commands. This
// controls how the play command interacts with the current playback state
// (replace, insert, etc.) and whether restrictions are overridden.
type PlayOptions struct {
	Reason               string `json:"reason,omitempty"`
	Operation            string `json:"operation,omitempty"`
	Trigger              string `json:"trigger,omitempty"`
	OverrideRestrictions bool   `json:"override_restrictions"`
	OnlyForLocalDevice   bool   `json:"only_for_local_device"`
	SystemInitiated      bool   `json:"system_initiated"`
}

// PlayerCommand is the command object nested inside a PlayerCommandRequest.
// It mirrors the dealer request command format used by librespot and the
// Spotify desktop client for remote device control.
//
// Endpoint path:
//
//	POST /connect-state/v1/player/command/from/{fromDevice}/to/{toDevice}
type PlayerCommand struct {
	// Endpoint is the command type: "resume", "pause", "skip_next",
	// "skip_prev", "seek_to", "set_shuffling_context",
	// "set_repeating_context", "set_repeating_track", "add_to_queue", etc.
	Endpoint string `json:"endpoint"`

	// Value is used by commands like seek_to (int), set_shuffling_context
	// (bool), set_repeating_context (bool), set_repeating_track (bool).
	Value interface{} `json:"value,omitempty"`

	// Position is used by seek_to for relative seeking.
	Position *int64 `json:"position,omitempty"`

	// Relative is used by seek_to: "beginning" or "current".
	Relative string `json:"relative,omitempty"`

	// Context is used by the "play" endpoint.
	Context *connectpb.Context `json:"context,omitempty"`

	// PlayOrigin is used by the "play" endpoint.
	PlayOrigin *connectpb.PlayOrigin `json:"play_origin,omitempty"`

	// Track is used by "skip_next" and "add_to_queue".
	Track *connectpb.ContextTrack `json:"track,omitempty"`

	// PrevTracks is used by "set_queue".
	PrevTracks []*connectpb.ContextTrack `json:"prev_tracks,omitempty"`

	// NextTracks is used by "set_queue".
	NextTracks []*connectpb.ContextTrack `json:"next_tracks,omitempty"`

	// Options carries additional command options.
	Options *CommandOptions `json:"options,omitempty"`

	// LoggingParams carries logging/telemetry metadata.
	LoggingParams *CommandLoggingParams `json:"logging_params,omitempty"`

	// ResumeOrigin is sent with "resume" commands.
	ResumeOrigin *ResumeOrigin `json:"resume_origin,omitempty"`

	// PreparePlayOptions is sent with "play" commands (e.g. playlist play).
	// It controls shuffle, skip-to, session, license, and other playback
	// preparation parameters. Format captured from cuts/playlist.bin.
	PreparePlayOptions *PreparePlayOptions `json:"prepare_play_options,omitempty"`

	// PlayOptions is sent with "play" commands (e.g. playlist play).
	// It controls how the command interacts with the current playback state.
	// Format captured from cuts/playlist.bin.
	PlayOptions *PlayOptions `json:"play_options,omitempty"`
}

// PlayerCommandRequest is the top-level JSON envelope sent to the
// connect-state player command endpoint. This matches the format observed
// from the Spotify desktop client:
//
//	{
//	  "command": { "endpoint": "resume", ... },
//	  "connection_type": "wlan",
//	  "intent_id": "<hex>"
//	}
//
// Note: the desktop client does NOT include message_id or sent_by_device_id
// at the top level. Those are part of the dealer WebSocket request format
// (used when commands are received), not when sending via the REST endpoint.
type PlayerCommandRequest struct {
	Command        *PlayerCommand `json:"command"`
	ConnectionType string         `json:"connection_type,omitempty"`
	IntentId       string         `json:"intent_id,omitempty"`
}

// RandomHex returns a random hex string of the given byte length (output is 2*n chars).
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TransferOptions mirrors librespot's TransferOptions struct used in the
// connect-state transfer endpoint body. All fields are optional string
// pointers matching the Rust serde serialization (skip_serializing_if =
// "Option::is_none").
type TransferOptions struct {
	RestorePaused   *string `json:"restore_paused,omitempty"`
	RestorePosition *string `json:"restore_position,omitempty"`
	RestoreTrack    *string `json:"restore_track,omitempty"`
	RetainSession   *string `json:"retain_session,omitempty"`
}

// TransferRequest is the JSON body POSTed to the connect-state transfer
// endpoint. It mirrors librespot's TransferRequest struct (see spclient.rs).
type TransferRequest struct {
	TransferOptions TransferOptions `json:"transfer_options"`
}

// Spclient is an HTTP client wrapper for Spotify's spclient API. It
// automatically injects bearer tokens (from Login5) and the client token into
// every request, retries on 401/502, and provides helpers for Connect State
// management and Web API proxying.
type Spclient struct {
	log spotcontrol.Logger

	client *http.Client

	baseUrl     *url.URL
	clientToken string
	deviceId    string

	accessToken spotcontrol.GetLogin5TokenFunc

	// warnedNoWebApiToken ensures the missing-OAuth2-token warning is logged
	// only once rather than on every Web API request.
	warnedNoWebApiToken sync.Once

	// webApiToken is an optional token function for Spotify Web API requests
	// (api.spotify.com). The Login5 token used by accessToken only works for
	// spclient endpoints; the Web API requires an OAuth2 token obtained via
	// the Authorization Code / PKCE flow with the appropriate scopes. When
	// set, WebApiRequest uses this instead of accessToken.
	webApiToken spotcontrol.GetLogin5TokenFunc
}

// NewSpclient creates a new Spclient. The addr function is called once to
// obtain the base URL (e.g. "spclient-wg.spotify.com:443"). If client is nil
// a default HTTP client with a 30-second timeout is used.
func NewSpclient(
	ctx context.Context,
	log spotcontrol.Logger,
	client *http.Client,
	addr spotcontrol.GetAddressFunc,
	accessToken spotcontrol.GetLogin5TokenFunc,
	deviceId, clientToken string,
) (*Spclient, error) {
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	baseUrl, err := url.Parse(fmt.Sprintf("https://%s/", addr(ctx)))
	if err != nil {
		return nil, fmt.Errorf("invalid spclient base url: %w", err)
	}

	return &Spclient{
		log:         log,
		client:      client,
		baseUrl:     baseUrl,
		clientToken: clientToken,
		deviceId:    deviceId,
		accessToken: accessToken,
	}, nil
}

// SetWebApiTokenFunc sets the token function used for Spotify Web API requests
// (api.spotify.com). If not set, WebApiRequest falls back to the Login5 token,
// which may lack the scopes required by Web API endpoints.
func (c *Spclient) SetWebApiTokenFunc(fn spotcontrol.GetLogin5TokenFunc) {
	c.webApiToken = fn
}

// retryAfterDuration parses the Retry-After header from an HTTP response. It
// supports both delta-seconds and HTTP-date formats. If the header is missing
// or unparseable a default fallback duration is returned.
func retryAfterDuration(resp *http.Response, fallback time.Duration) time.Duration {
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return fallback
	}

	// Try delta-seconds first.
	if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}

	// Try HTTP-date (RFC 1123).
	if t, err := time.Parse(time.RFC1123, ra); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}

	return fallback
}

const (
	maxAuthRetries              = 2                // max times we'll force a new token on 401
	maxTotalRetries      uint64 = 5                // absolute cap on retry attempts
	maxRetryAfterWait           = 60 * time.Second // don't wait longer than this for Retry-After
	defaultRateLimitWait        = 5 * time.Second  // fallback when Retry-After header is missing
)

// innerRequest is the shared implementation for both spclient and Web API
// requests. It injects the client token and bearer token, handles 401 retries
// (by forcing a new access token), retries 502 responses, and respects 429
// rate-limit responses with Retry-After back-off.
func (c *Spclient) innerRequest(
	ctx context.Context,
	method string,
	reqUrl *url.URL,
	query url.Values,
	header http.Header,
	body []byte,
) (*http.Response, error) {
	return c.innerRequestWithToken(ctx, method, reqUrl, query, header, body, c.accessToken)
}

// innerRequestWithToken is the shared implementation for both spclient and Web
// API requests. It injects the client token and bearer token (from the given
// tokenFunc), handles 401 retries (by forcing a new access token), retries
// 502 responses, and respects 429 rate-limit responses with Retry-After
// back-off.
func (c *Spclient) innerRequestWithToken(
	ctx context.Context,
	method string,
	reqUrl *url.URL,
	query url.Values,
	header http.Header,
	body []byte,
	tokenFunc spotcontrol.GetLogin5TokenFunc,
) (*http.Response, error) {
	if query != nil {
		reqUrl.RawQuery = query.Encode()
	}

	req := &http.Request{
		URL:    reqUrl,
		Method: method,
		Header: http.Header{},
	}

	if header != nil {
		for name, values := range header {
			req.Header[name] = values
		}
	}

	req.Header.Set("Client-Token", c.clientToken)
	req.Header.Set("User-Agent", spotcontrol.UserAgent())

	if body != nil {
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/x-protobuf")
		}

		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		req.Body, _ = req.GetBody()
	}

	var forceNewToken bool
	var authRetries int

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 500 * time.Millisecond
	bo.MaxInterval = 10 * time.Second
	bo.MaxElapsedTime = 90 * time.Second

	resp, err := backoff.RetryWithData(func() (*http.Response, error) {
		accessToken, err := tokenFunc(ctx, forceNewToken)
		if err != nil {
			return nil, backoff.Permanent(fmt.Errorf("failed obtaining spclient access token: %w", err))
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))

		// Reset the body for retries.
		if req.GetBody != nil {
			req.Body, _ = req.GetBody()
		}

		resp, err := c.client.Do(req.WithContext(ctx))
		if err != nil {
			return nil, err
		}

		switch resp.StatusCode {
		case 401:
			_ = resp.Body.Close()
			authRetries++
			if authRetries > maxAuthRetries {
				return nil, backoff.Permanent(fmt.Errorf("unauthorized after %d token refreshes", authRetries))
			}
			forceNewToken = true
			c.log.Debugf("spclient request returned 401, refreshing token (attempt %d/%d)", authRetries, maxAuthRetries)
			return nil, fmt.Errorf("unauthorized")

		case 429:
			_ = resp.Body.Close()
			wait := retryAfterDuration(resp, defaultRateLimitWait)
			if wait > maxRetryAfterWait {
				wait = maxRetryAfterWait
			}
			c.log.Debugf("spclient request rate limited (429), waiting %s before retry", wait)

			// Sleep for the Retry-After duration, respecting context cancellation.
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, backoff.Permanent(ctx.Err())
			}
			return nil, fmt.Errorf("rate limited")

		case 502, 503:
			_ = resp.Body.Close()
			c.log.Debugf("spclient request returned %d, retrying...", resp.StatusCode)
			return nil, fmt.Errorf("server error %d", resp.StatusCode)
		}

		return resp, nil
	}, backoff.WithContext(backoff.WithMaxRetries(bo, maxTotalRetries), ctx))
	if err != nil {
		return nil, fmt.Errorf("spclient request failed: %w", err)
	}

	return resp, nil
}

// Request sends an HTTP request to the spclient base URL with the given path.
// The caller is responsible for closing the response body.
func (c *Spclient) Request(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	header http.Header,
	body []byte,
) (*http.Response, error) {
	reqUrl := c.baseUrl.JoinPath(path)
	return c.innerRequest(ctx, method, reqUrl, query, header, body)
}

// WebApiRequest sends an HTTP request to the Spotify Web API
// (https://api.spotify.com/) with the given path. Bearer and client tokens are
// automatically injected. The caller is responsible for closing the response
// body.
func (c *Spclient) WebApiRequest(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	header http.Header,
	body []byte,
) (*http.Response, error) {
	reqPath, err := url.Parse("https://api.spotify.com/")
	if err != nil {
		panic("invalid api base url")
	}
	reqURL := reqPath.JoinPath(path)

	// Use the OAuth2 Web API token if available; otherwise fall back to
	// the Login5 token (which may not have the right scopes).
	tokenFunc := c.accessToken
	if c.webApiToken != nil {
		tokenFunc = c.webApiToken
	} else {
		c.warnedNoWebApiToken.Do(func() {
			c.log.Warnf("no OAuth2 Web API token configured; falling back to Login5 token for api.spotify.com requests (this may fail — run with --interactive to obtain a proper OAuth2 token)")
		})
	}
	return c.innerRequestWithToken(ctx, method, reqURL, query, header, body, tokenFunc)
}

// GetAccessToken returns an access token, optionally forcing a new one.
func (c *Spclient) GetAccessToken(ctx context.Context, force bool) (string, error) {
	return c.accessToken(ctx, force)
}

// putStateError is the JSON error body returned by the connect-state PUT
// endpoint on failure.
type putStateError struct {
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
}

// PutConnectStateInactive marks this device as inactive in the connect state.
func (c *Spclient) PutConnectStateInactive(ctx context.Context, spotConnId string, notify bool) error {
	resp, err := c.Request(
		ctx,
		"PUT",
		fmt.Sprintf("/connect-state/v1/devices/%s/inactive", c.deviceId),
		url.Values{"notify": []string{strconv.FormatBool(notify)}},
		http.Header{
			"X-Spotify-Connection-Id": []string{spotConnId},
		},
		nil,
	)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return fmt.Errorf("put state inactive request failed with status %d", resp.StatusCode)
	}

	c.log.Debug("put connect state inactive")
	return nil
}

// PutConnectState sends a PutStateRequest to register or update this device's
// connect state. The spotConnId is the X-Spotify-Connection-Id obtained from
// the dealer WebSocket connection.
//
// On success it returns the raw response body, which is a serialized Cluster
// protobuf that the caller can unmarshal to obtain the initial cluster state
// (including all visible devices and the active device).
func (c *Spclient) PutConnectState(ctx context.Context, spotConnId string, reqProto *connectpb.PutStateRequest) ([]byte, error) {
	reqBody, err := proto.Marshal(reqProto)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling PutStateRequest: %w", err)
	}

	respBody, err := backoff.RetryWithData(func() ([]byte, error) {
		resp, err := c.Request(
			ctx,
			"PUT",
			fmt.Sprintf("/connect-state/v1/devices/%s", c.deviceId),
			nil,
			http.Header{
				"X-Spotify-Connection-Id": []string{spotConnId},
				"Content-Type":            []string{"application/x-protobuf"},
			},
			reqBody,
		)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("failed reading response body: %w", readErr)
		}

		if resp.StatusCode != 200 {
			var putError putStateError
			if decErr := json.Unmarshal(body, &putError); decErr != nil {
				c.log.Debugf("failed reading error response: %s", decErr)
				return nil, fmt.Errorf("put state request failed with status %d (unreadable body)", resp.StatusCode)
			}
			c.log.Debugf("put state request failed with status %d: %s", resp.StatusCode, putError.Message)
			return nil, fmt.Errorf("put state request failed with status %d: %s", resp.StatusCode, putError.Message)
		}

		c.log.Debugf("put connect state because %s", reqProto.PutStateReason)
		return body, nil
	}, backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Second), 2), ctx))

	return respBody, err
}

// DeviceId returns the device ID this Spclient was created with.
func (c *Spclient) DeviceId() string {
	return c.deviceId
}

// ConnectPlayerCommand sends a playback command to a remote device through the
// connect-state player command endpoint. This is the same mechanism used by the
// Spotify desktop client to control remote devices.
//
// The endpoint is:
//
//	POST /connect-state/v1/player/command/from/{fromDevice}/to/{toDevice}
//
// The body is a gzip-compressed JSON-encoded PlayerCommandRequest. The format
// was determined by capturing traffic from the Spotify desktop client:
//
//	{
//	  "command": {"endpoint": "resume", "options": {...}, "logging_params": {...}},
//	  "connection_type": "wlan",
//	  "intent_id": "<random hex>"
//	}
func (c *Spclient) ConnectPlayerCommand(
	ctx context.Context,
	spotConnId string,
	targetDeviceId string,
	cmdReq *PlayerCommandRequest,
) error {
	jsonBody, err := json.Marshal(cmdReq)
	if err != nil {
		return fmt.Errorf("failed marshalling player command request: %w", err)
	}

	// Gzip-compress the JSON body, matching the desktop client behavior.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(jsonBody); err != nil {
		return fmt.Errorf("failed gzip compressing player command: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("failed closing gzip writer: %w", err)
	}

	path := fmt.Sprintf(
		"/connect-state/v1/player/command/from/%s/to/%s",
		c.deviceId, targetDeviceId,
	)

	resp, err := c.Request(ctx, "POST", path, nil, http.Header{
		"X-Spotify-Connection-Id": []string{spotConnId},
		"Content-Type":            []string{"application/json"},
		"Content-Encoding":        []string{"gzip"},
	}, buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed sending connect player command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case 200, 202, 204:
		return nil
	default:
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("connect player command (%s) failed with status %d: %s",
			cmdReq.Command.Endpoint, resp.StatusCode, string(respBody))
	}
}

// ConnectTransfer triggers a playback transfer through the connect-state
// transfer endpoint. This is the same endpoint librespot uses (see
// spclient.rs transfer()).
//
// Using the same device ID for both from and to initiates the transfer from
// the currently active device to this device.
//
// The transferReq parameter mirrors librespot's TransferRequest struct. Pass
// nil to send an empty POST (no body). The endpoint path is:
//
//	POST /connect-state/v1/connect/transfer/from/{fromDeviceId}/to/{toDeviceId}
func (c *Spclient) ConnectTransfer(
	ctx context.Context,
	spotConnId string,
	fromDeviceId string,
	toDeviceId string,
	transferReq *TransferRequest,
) error {
	path := fmt.Sprintf(
		"/connect-state/v1/connect/transfer/from/%s/to/%s",
		fromDeviceId, toDeviceId,
	)

	var body []byte
	if transferReq != nil {
		var err error
		body, err = json.Marshal(transferReq)
		if err != nil {
			return fmt.Errorf("failed marshalling transfer request: %w", err)
		}
	}

	header := http.Header{
		"X-Spotify-Connection-Id": []string{spotConnId},
	}
	if body != nil {
		header.Set("Content-Type", "application/json")
	}

	resp, err := c.Request(ctx, "POST", path, nil, header, body)
	if err != nil {
		return fmt.Errorf("failed sending connect transfer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case 200, 202, 204:
		return nil
	default:
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("connect transfer failed with status %d: %s",
			resp.StatusCode, string(respBody))
	}
}

// ConnectSetVolume sends a volume change through the connect-state volume
// endpoint. This is the dedicated volume signaling path used by librespot
// (see SetVolumeCommand handling in spirc.rs) and is separate from the
// general player command endpoint.
func (c *Spclient) ConnectSetVolume(
	ctx context.Context,
	spotConnId string,
	targetDeviceId string,
	volume int32,
) error {
	volumeCmd := &connectpb.SetVolumeCommand{
		Volume: volume,
	}

	body, err := proto.Marshal(volumeCmd)
	if err != nil {
		return fmt.Errorf("failed marshalling SetVolumeCommand: %w", err)
	}

	path := fmt.Sprintf(
		"/connect-state/v1/connect/volume/from/%s/to/%s",
		c.deviceId, targetDeviceId,
	)

	resp, err := c.Request(ctx, "PUT", path, nil, http.Header{
		"X-Spotify-Connection-Id": []string{spotConnId},
		"Content-Type":            []string{"application/x-protobuf"},
	}, body)
	if err != nil {
		return fmt.Errorf("failed sending connect volume: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case 200, 202, 204:
		return nil
	default:
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("connect volume command failed with status %d: %s",
			resp.StatusCode, string(respBody))
	}
}
