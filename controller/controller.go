package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	spotcontrol "github.com/badfortrains/spotcontrol"
	"github.com/badfortrains/spotcontrol/dealer"
	connectpb "github.com/badfortrains/spotcontrol/proto/spotify/connectstate"
	devicespb "github.com/badfortrains/spotcontrol/proto/spotify/connectstate/devices"
	"github.com/badfortrains/spotcontrol/spclient"
	"google.golang.org/protobuf/proto"
)

// DeviceInfo represents a Spotify Connect device visible on the account.
type DeviceInfo struct {
	// Id is the unique device identifier.
	Id string
	// Name is the human-readable device name.
	Name string
	// Type is the device type (computer, smartphone, speaker, etc.).
	Type string
	// IsActive is true if this device is the currently active playback device.
	IsActive bool
	// Volume is the device volume (0-65535 for connect state, 0-100 for Web API).
	Volume int
	// SupportsVolume is true if the device supports volume control.
	SupportsVolume bool
}

// PlayerState represents the current playback state of the account.
type PlayerState struct {
	// IsPlaying is true if a track is currently playing (not paused).
	IsPlaying bool
	// TrackURI is the Spotify URI of the current track (e.g. "spotify:track:...").
	TrackURI string
	// ContextURI is the Spotify URI of the current playback context (album, playlist, etc.).
	ContextURI string
	// PositionMs is the current playback position in milliseconds.
	PositionMs int64
	// DurationMs is the total duration of the current track in milliseconds.
	DurationMs int64
	// DeviceId is the ID of the device currently playing.
	DeviceId string
	// Shuffle is true if shuffle is enabled.
	Shuffle bool
	// RepeatContext is true if context repeat is enabled.
	RepeatContext bool
	// RepeatTrack is true if track repeat is enabled.
	RepeatTrack bool
}

// Controller is the high-level public API for Spotify Connect device control.
// It provides methods for listing devices, controlling playback (play, pause,
// skip, volume), loading tracks, and transferring playback between devices.
//
// Controller maintains a cached view of the account's device cluster obtained
// from the connect-state dealer WebSocket push messages. It also provides
// direct Web API queries as an alternative.
//
// Controller is safe for concurrent use.
type Controller struct {
	log spotcontrol.Logger

	sp       *spclient.Spclient
	dealer   *dealer.Dealer
	deviceId string

	deviceName string
	deviceType devicespb.DeviceType

	// cluster is the most recently received connect-state cluster update.
	cluster     *connectpb.Cluster
	clusterLock sync.RWMutex

	// spotConnId is the X-Spotify-Connection-Id from the pusher Mercury event,
	// needed for PutConnectState.
	spotConnId     string
	spotConnIdLock sync.RWMutex

	// registered tracks whether RegisterDevice has been called successfully
	// at least once during this session.
	registered bool

	stopCh chan struct{}
	once   sync.Once
}

// Config holds the configuration for creating a new Controller.
type Config struct {
	// Log is the logger to use. If nil, a NullLogger is used.
	Log spotcontrol.Logger

	// Spclient is the spclient HTTP wrapper (from Session.Spclient()).
	Spclient *spclient.Spclient

	// Dealer is the dealer WebSocket client (from Session.Dealer()).
	Dealer *dealer.Dealer

	// DeviceId is this controller's device identifier.
	DeviceId string

	// DeviceName is the human-readable name for this controller device.
	DeviceName string

	// DeviceType is the Spotify device type for this controller.
	DeviceType devicespb.DeviceType
}

// NewController creates a new Controller and starts listening for cluster
// updates from the dealer. The dealer must be connected before calling this
// (call Session.Dealer().Connect() first).
//
// After creation, call RegisterDevice to announce this controller to the
// Spotify connect-state backend.
func NewController(cfg Config) *Controller {
	log := cfg.Log
	if log == nil {
		log = &spotcontrol.NullLogger{}
	}

	c := &Controller{
		log:        log,
		sp:         cfg.Spclient,
		dealer:     cfg.Dealer,
		deviceId:   cfg.DeviceId,
		deviceName: cfg.DeviceName,
		deviceType: cfg.DeviceType,
		stopCh:     make(chan struct{}),
	}

	return c
}

// Start connects the dealer (if not already connected), subscribes to the
// pusher connection ID message and cluster update messages on the dealer, and
// begins processing them in the background. It should be called after
// NewController.
//
// The Spotify-Connection-Id is delivered as the first dealer WebSocket message
// (URI prefix hm://pusher/v1/connections/) with the actual ID in the
// Spotify-Connection-Id message header. Once received, Start automatically
// calls RegisterDevice to announce this controller to the connect-state
// backend. Subsequent connection ID messages (e.g. after dealer reconnection)
// trigger re-registration.
func (c *Controller) Start(ctx context.Context) error {
	// Connect the dealer.
	if err := c.dealer.Connect(ctx); err != nil {
		return fmt.Errorf("failed connecting dealer: %w", err)
	}

	// Subscribe to dealer messages we care about:
	//   - hm://pusher/v1/connections/ → carries the Spotify-Connection-Id
	//   - hm://connect-state/v1/cluster → cluster state updates
	connCh := c.dealer.ReceiveMessage(
		"hm://pusher/v1/connections/",
	)
	clusterCh := c.dealer.ReceiveMessage(
		"hm://connect-state/v1/cluster",
	)

	// Start the background listeners.
	go c.connectionIdLoop(connCh)
	go c.clusterUpdateLoop(clusterCh)

	return nil
}

// connectionIdLoop processes dealer messages that carry the
// Spotify-Connection-Id. On each new connection ID it stores the value and
// calls RegisterDevice to (re-)announce this controller.
func (c *Controller) connectionIdLoop(ch <-chan dealer.Message) {
	for {
		select {
		case <-c.stopCh:
			return
		case msg, ok := <-ch:
			if !ok {
				c.log.Debug("connection ID channel closed")
				return
			}
			c.handleConnectionMessage(msg)
		}
	}
}

// handleConnectionMessage extracts the Spotify-Connection-Id from a dealer
// pusher message and registers (or re-registers) the device.
func (c *Controller) handleConnectionMessage(msg dealer.Message) {
	// The connection ID is in the Spotify-Connection-Id header of the dealer
	// message (not in the URI or payload).
	connId := msg.Headers["Spotify-Connection-Id"]
	if connId == "" {
		c.log.Warnf("pusher connection message has no Spotify-Connection-Id header: uri=%s headers=%v", msg.Uri, msg.Headers)
		return
	}

	c.log.Infof("received Spotify-Connection-Id from dealer (%d bytes)", len(connId))

	c.spotConnIdLock.Lock()
	c.spotConnId = connId
	c.spotConnIdLock.Unlock()

	// Register (or re-register) the device with the connect-state backend.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.RegisterDevice(ctx, connId); err != nil {
		c.log.WithError(err).Errorf("failed registering device with connect-state")
	} else {
		c.registered = true
		c.log.Infof("device registered with connect-state (name=%s, id=%s)", c.deviceName, c.deviceId)
	}
}

// RegisterDevice announces this controller as a new device to the Spotify
// connect-state backend by sending a PutConnectState with reason NEW_DEVICE.
// This causes the device to appear in the Spotify Connect device list.
//
// The spotConnId is the X-Spotify-Connection-Id that identifies the dealer
// WebSocket session. Typically obtained from the dealer connection headers.
func (c *Controller) RegisterDevice(ctx context.Context, spotConnId string) error {
	c.spotConnIdLock.Lock()
	c.spotConnId = spotConnId
	c.spotConnIdLock.Unlock()

	req := &connectpb.PutStateRequest{
		MemberType:     connectpb.MemberType_CONNECT_STATE,
		PutStateReason: connectpb.PutStateReason_NEW_DEVICE,
		Device: &connectpb.Device{
			DeviceInfo: &connectpb.DeviceInfo{
				CanPlay:               true,
				DeviceId:              c.deviceId,
				DeviceType:            c.deviceType,
				Name:                  c.deviceName,
				DeviceSoftwareVersion: "spotcontrol/" + spotcontrol.VersionNumberString(),
				ClientId:              spotcontrol.ClientIdHex,
				SpircVersion:          "3.2.6",
				Capabilities: &connectpb.Capabilities{
					CanBePlayer:             false,
					GaiaEqConnectId:         true,
					SupportsLogout:          false,
					IsObservable:            true,
					VolumeSteps:             64,
					SupportedTypes:          []string{"audio/track", "audio/episode"},
					CommandAcks:             true,
					SupportsRename:          false,
					Hidden:                  false,
					DisableVolume:           false,
					SupportsPlaylistV2:      true,
					IsControllable:          true,
					SupportsTransferCommand: true,
					SupportsCommandRequest:  true,
					SupportsGzipPushes:      true,
				},
			},
			// Include an initialized PlayerState with sensible defaults.
			// go-librespot always includes a PlayerState in the Device when
			// announcing via PutConnectState; omitting it can cause the
			// backend to reject the request or behave unexpectedly.
			PlayerState: &connectpb.PlayerState{
				PlaybackSpeed: 1.0,
				IsPlaying:     false,
				IsPaused:      false,
				IsBuffering:   false,
				Options: &connectpb.ContextPlayerOptions{
					ShufflingContext: false,
					RepeatingContext: false,
					RepeatingTrack:   false,
				},
				Suppressions: &connectpb.Suppressions{},
			},
		},
		ClientSideTimestamp: uint64(time.Now().UnixMilli()),
	}

	respBody, err := c.sp.PutConnectState(ctx, spotConnId, req)
	if err != nil {
		return err
	}

	// The PutConnectState response body is a serialized Cluster protobuf.
	// Parse it to immediately populate the cached cluster so that device
	// listing works right away without waiting for a dealer push.
	if len(respBody) > 0 {
		var cluster connectpb.Cluster
		if unmarshalErr := proto.Unmarshal(respBody, &cluster); unmarshalErr != nil {
			c.log.Warnf("failed to unmarshal initial cluster from PutConnectState response: %v", unmarshalErr)
		} else {
			c.clusterLock.Lock()
			c.cluster = &cluster
			c.clusterLock.Unlock()

			c.log.Infof("initial cluster loaded: active_device=%s, devices=%d",
				cluster.GetActiveDeviceId(),
				len(cluster.GetDevice()),
			)
		}
	}

	return nil
}

// SetConnectionId updates the dealer connection ID. This should be called
// whenever the dealer reconnects and a new connection ID is obtained.
func (c *Controller) SetConnectionId(id string) {
	c.spotConnIdLock.Lock()
	c.spotConnId = id
	c.spotConnIdLock.Unlock()
}

// connectionId returns the current dealer connection ID.
func (c *Controller) connectionId() string {
	c.spotConnIdLock.RLock()
	defer c.spotConnIdLock.RUnlock()
	return c.spotConnId
}

// clusterUpdateLoop processes cluster update messages from the dealer in the
// background.
func (c *Controller) clusterUpdateLoop(ch <-chan dealer.Message) {
	for {
		select {
		case <-c.stopCh:
			return
		case msg, ok := <-ch:
			if !ok {
				c.log.Debug("cluster update channel closed")
				return
			}
			c.handleClusterUpdate(msg)
		}
	}
}

// handleClusterUpdate parses a ClusterUpdate protobuf from a dealer message
// and caches the cluster state.
func (c *Controller) handleClusterUpdate(msg dealer.Message) {
	var update connectpb.ClusterUpdate
	if err := proto.Unmarshal(msg.Payload, &update); err != nil {
		c.log.WithError(err).Error("failed unmarshalling ClusterUpdate")
		return
	}

	c.clusterLock.Lock()
	c.cluster = update.Cluster
	c.clusterLock.Unlock()

	c.log.Debugf("received cluster update: reason=%s, active_device=%s, devices=%d",
		update.UpdateReason,
		update.Cluster.GetActiveDeviceId(),
		len(update.Cluster.GetDevice()),
	)
}

// Close stops the controller's background processing. After Close returns the
// controller should not be used.
func (c *Controller) Close() {
	c.once.Do(func() {
		close(c.stopCh)
	})
}

// ---------------------------------------------------------------------------
// Device listing
// ---------------------------------------------------------------------------

// ListDevices returns the list of devices from the cached connect-state
// cluster. The cluster is updated in real-time via the dealer WebSocket.
// Returns nil if no cluster data is available yet.
func (c *Controller) ListDevices() []DeviceInfo {
	c.clusterLock.RLock()
	cluster := c.cluster
	c.clusterLock.RUnlock()

	if cluster == nil {
		return nil
	}

	activeId := cluster.GetActiveDeviceId()
	devices := make([]DeviceInfo, 0, len(cluster.Device))
	for id, info := range cluster.Device {
		di := DeviceInfo{
			Id:             id,
			Name:           info.GetName(),
			Type:           info.GetDeviceType().String(),
			IsActive:       id == activeId,
			Volume:         int(info.GetVolume()),
			SupportsVolume: info.GetCapabilities() != nil && !info.GetCapabilities().GetDisableVolume(),
		}
		devices = append(devices, di)
	}

	return devices
}

// ListDevicesFromAPI queries the Spotify Web API for the current list of
// available devices. This is more accurate than the cached cluster state but
// requires a network request.
func (c *Controller) ListDevicesFromAPI(ctx context.Context) ([]DeviceInfo, error) {
	resp, err := c.sp.WebApiRequest(ctx, "GET", "/v1/me/player/devices", nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed querying devices API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("devices API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Devices []struct {
			Id               string `json:"id"`
			IsActive         bool   `json:"is_active"`
			IsPrivateSession bool   `json:"is_private_session"`
			IsRestricted     bool   `json:"is_restricted"`
			Name             string `json:"name"`
			Type             string `json:"type"`
			VolumePercent    int    `json:"volume_percent"`
			SupportsVolume   bool   `json:"supports_volume"`
		} `json:"devices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed decoding devices response: %w", err)
	}

	devices := make([]DeviceInfo, len(result.Devices))
	for i, d := range result.Devices {
		devices[i] = DeviceInfo{
			Id:             d.Id,
			Name:           d.Name,
			Type:           d.Type,
			IsActive:       d.IsActive,
			Volume:         d.VolumePercent,
			SupportsVolume: d.SupportsVolume,
		}
	}

	return devices, nil
}

// GetPlayerState queries the Spotify Web API for the current playback state.
func (c *Controller) GetPlayerState(ctx context.Context) (*PlayerState, error) {
	resp, err := c.sp.WebApiRequest(ctx, "GET", "/v1/me/player", nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed querying player state API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 204 {
		// No active playback.
		return nil, nil
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("player state API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		IsPlaying  bool   `json:"is_playing"`
		ProgressMs int64  `json:"progress_ms"`
		ShuffleOn  bool   `json:"shuffle_state"`
		RepeatMode string `json:"repeat_state"`
		Device     struct {
			Id string `json:"id"`
		} `json:"device"`
		Item struct {
			Uri        string `json:"uri"`
			DurationMs int64  `json:"duration_ms"`
		} `json:"item"`
		Context struct {
			Uri string `json:"uri"`
		} `json:"context"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed decoding player state response: %w", err)
	}

	return &PlayerState{
		IsPlaying:     result.IsPlaying,
		TrackURI:      result.Item.Uri,
		ContextURI:    result.Context.Uri,
		PositionMs:    result.ProgressMs,
		DurationMs:    result.Item.DurationMs,
		DeviceId:      result.Device.Id,
		Shuffle:       result.ShuffleOn,
		RepeatContext: result.RepeatMode == "context",
		RepeatTrack:   result.RepeatMode == "track",
	}, nil
}

// ---------------------------------------------------------------------------
// Playback control (via Web API through spclient)
// ---------------------------------------------------------------------------

// Play resumes playback on the currently active device, or on the specified
// device if deviceId is non-empty.
func (c *Controller) Play(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player/play", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending play command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("play command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Pause pauses playback on the currently active device, or on the specified
// device if deviceId is non-empty.
func (c *Controller) Pause(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player/pause", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending pause command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pause command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Next skips to the next track.
func (c *Controller) Next(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "POST", "/v1/me/player/next", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending next command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("next command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Previous skips to the previous track.
func (c *Controller) Previous(ctx context.Context, deviceId string) error {
	query := url.Values{}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "POST", "/v1/me/player/previous", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending previous command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("previous command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SetVolume sets the playback volume to the given percentage (0-100).
func (c *Controller) SetVolume(ctx context.Context, volumePercent int, deviceId string) error {
	if volumePercent < 0 {
		volumePercent = 0
	}
	if volumePercent > 100 {
		volumePercent = 100
	}

	query := url.Values{
		"volume_percent": []string{fmt.Sprintf("%d", volumePercent)},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player/volume", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending volume command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("volume command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Seek seeks to the given position in the current track.
func (c *Controller) Seek(ctx context.Context, positionMs int64, deviceId string) error {
	query := url.Values{
		"position_ms": []string{fmt.Sprintf("%d", positionMs)},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player/seek", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending seek command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("seek command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SetShuffle enables or disables shuffle mode.
func (c *Controller) SetShuffle(ctx context.Context, state bool, deviceId string) error {
	query := url.Values{
		"state": []string{fmt.Sprintf("%t", state)},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player/shuffle", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending shuffle command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("shuffle command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SetRepeat sets the repeat mode. Valid values are "off", "context", and "track".
func (c *Controller) SetRepeat(ctx context.Context, state string, deviceId string) error {
	query := url.Values{
		"state": []string{state},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player/repeat", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending repeat command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("repeat command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

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
// "spotify:track:6rqhFgbbKwnb9MLmUQDhG6") on the specified device. If opts
// is nil, sensible defaults are used.
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

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player/play", query, header, bodyBytes)
	if err != nil {
		return fmt.Errorf("failed sending load track command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("load track command failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// TransferPlayback transfers playback to the specified device. If play is true,
// playback starts immediately on the target device; otherwise the device becomes
// active but remains paused.
func (c *Controller) TransferPlayback(ctx context.Context, deviceId string, play bool) error {
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

	resp, err := c.sp.WebApiRequest(ctx, "PUT", "/v1/me/player", nil, header, bodyBytes)
	if err != nil {
		return fmt.Errorf("failed sending transfer playback command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("transfer playback command failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// AddToQueue adds a track to the user's playback queue.
func (c *Controller) AddToQueue(ctx context.Context, trackURI string, deviceId string) error {
	query := url.Values{
		"uri": []string{trackURI},
	}
	if deviceId != "" {
		query.Set("device_id", deviceId)
	}

	resp, err := c.sp.WebApiRequest(ctx, "POST", "/v1/me/player/queue", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending add to queue command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add to queue command failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ---------------------------------------------------------------------------
// Cluster state accessors
// ---------------------------------------------------------------------------

// Cluster returns the most recently cached connect-state cluster, or nil if
// no cluster update has been received yet.
func (c *Controller) Cluster() *connectpb.Cluster {
	c.clusterLock.RLock()
	defer c.clusterLock.RUnlock()
	return c.cluster
}

// ActiveDeviceId returns the ID of the currently active playback device from
// the cached cluster state, or an empty string if unknown.
func (c *Controller) ActiveDeviceId() string {
	c.clusterLock.RLock()
	defer c.clusterLock.RUnlock()

	if c.cluster == nil {
		return ""
	}
	return c.cluster.GetActiveDeviceId()
}
