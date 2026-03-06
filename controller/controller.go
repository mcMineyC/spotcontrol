package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	"github.com/mcMineyC/spotcontrol/dealer"
	connectpb "github.com/mcMineyC/spotcontrol/proto/spotify/connectstate"
	devicespb "github.com/mcMineyC/spotcontrol/proto/spotify/connectstate/devices"
	"github.com/mcMineyC/spotcontrol/spclient"

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
	// Volume is the device volume as a percentage (0-100). Connect-state values
	// are automatically normalized from the internal 0-65535 range.
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
// By default, playback control commands are sent through the spclient proxy
// which routes Web API style requests (/v1/me/player/*) through the spclient
// infrastructure rather than the public api.spotify.com endpoint. This avoids
// the public Web API rate limits.
//
// If UseWebApi is set in Config, commands are sent directly to
// api.spotify.com instead (not recommended — subject to stricter rate limits).
//
// The connect-state transfer endpoint is used for playback transfers, matching
// librespot's transfer() implementation.
//
// The connect-state volume endpoint is used for volume changes, matching
// librespot's SetVolumeCommand handling.
//
// Volume updates are debounced (500ms) following librespot's approach to
// prevent rate limiting from rapid volume adjustments.
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

	// useWebApi controls whether playback commands are routed directly to the
	// public Web API (api.spotify.com) or through the spclient proxy. When
	// false (the default), commands use the spclient proxy which routes
	// /v1/me/player/* requests through the spclient infrastructure, avoiding
	// the stricter public API rate limits.
	useWebApi bool

	// --- volume debouncing (matches librespot's VOLUME_UPDATE_DELAY) ---
	volumeMu          sync.Mutex
	pendingVolume     int           // the volume value waiting to be sent
	pendingVolumeId   string        // target device for the pending volume
	volumeTimer       *time.Timer   // fires after volumeDebounceDelay
	volumeDebouncing  bool          // true when a debounced send is pending
	volumeDebounceDur time.Duration // configurable, default 500ms

	// --- event subscribers ---
	subs eventSubscribers

	// --- track change detection & metadata cache ---
	lastTrackURI string
	lastTrackMu  sync.Mutex
	lastMeta     *TrackMetadata
	lastMetaMu   sync.RWMutex

	stopCh chan struct{}
	once   sync.Once
}

// volumeDebounceDefault is the default delay before a debounced volume command
// is actually sent. Matches librespot's VOLUME_UPDATE_DELAY (500ms).
const volumeDebounceDefault = 500 * time.Millisecond

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

	// UseWebApi forces playback commands to be routed directly to the public
	// Web API (api.spotify.com) instead of through the spclient proxy. This
	// is not recommended because api.spotify.com has stricter rate limits.
	// Default false.
	UseWebApi bool

	// VolumeDebounce overrides the default volume debounce duration. If zero,
	// the default of 500ms (matching librespot) is used. Set to a negative
	// value to disable debouncing.
	VolumeDebounce time.Duration
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

	vd := cfg.VolumeDebounce
	if vd == 0 {
		vd = volumeDebounceDefault
	}

	c := &Controller{
		log:               log,
		sp:                cfg.Spclient,
		dealer:            cfg.Dealer,
		deviceId:          cfg.DeviceId,
		deviceName:        cfg.DeviceName,
		deviceType:        cfg.DeviceType,
		useWebApi:         cfg.UseWebApi,
		volumeDebounceDur: vd,
		stopCh:            make(chan struct{}),
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

// ---------------------------------------------------------------------------
// Connection ID handling
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Device registration
// ---------------------------------------------------------------------------

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

			// Emit initial events so subscribers see the starting state
			// without waiting for the first dealer push.
			c.processClusterUpdate(&cluster, connectpb.ClusterUpdateReason_NEW_DEVICE_APPEARED, nil)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Cluster state management
// ---------------------------------------------------------------------------

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

// handleClusterUpdate parses a ClusterUpdate protobuf from a dealer message,
// caches the cluster state, and emits events to subscriber channels.
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

	// Emit events to subscriber channels.
	c.processClusterUpdate(update.Cluster, update.GetUpdateReason(), update.GetDevicesThatChanged())
}

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

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Close stops the controller's background processing. After Close returns the
// controller should not be used.
func (c *Controller) Close() {
	c.once.Do(func() {
		close(c.stopCh)

		// Stop any pending volume debounce timer.
		c.volumeMu.Lock()
		if c.volumeTimer != nil {
			c.volumeTimer.Stop()
		}
		c.volumeMu.Unlock()

		// Close all event subscriber channels.
		c.closeAllSubscribers()
	})
}
