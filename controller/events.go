package controller

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	spotcontrol "github.com/mcMineyC/spotcontrol"
	connectpb "github.com/mcMineyC/spotcontrol/proto/spotify/connectstate"
	"github.com/mcMineyC/spotcontrol/spclient"
)

// ---------------------------------------------------------------------------
// Track metadata (enriched from the private spclient metadata API)
// ---------------------------------------------------------------------------

// TrackMetadata holds rich metadata for the currently playing track. It is
// assembled from the connect-state cluster (for URI/duration/position) and
// the private spclient metadata API (for title, artist, album, cover art).
//
// The metadata API endpoint is:
//
//	GET /metadata/4/track/{hex_id}?market=from_token
//
// which returns JSON with title, album (including cover art URLs), artists,
// duration, and more. It uses the Login5 bearer token — no OAuth2 Web API
// token is needed.
type TrackMetadata struct {
	// TrackURI is the Spotify URI (e.g. "spotify:track:5TFCp6cxCaJRhbdn6IWEGh").
	TrackURI string

	// Title is the track title.
	Title string

	// Artist is the primary artist name.
	Artist string

	// Album is the album name.
	Album string

	// DurationMs is the track duration in milliseconds.
	DurationMs int64

	// ImageURL is the URL of the album cover art (largest available).
	ImageURL string

	// SmallImageURL is the URL of the small (64px) cover art.
	SmallImageURL string

	// ArtistURI is the Spotify URI for the primary artist, if available.
	ArtistURI string

	// AlbumURI is the Spotify URI for the album, if available.
	AlbumURI string

	// Raw is the full raw metadata response from the private API, available
	// for callers who need additional fields (cover images at all sizes,
	// external IDs, release date, etc.).
	Raw *spclient.TrackMetadata
}

// ---------------------------------------------------------------------------
// Event types delivered through channels
// ---------------------------------------------------------------------------

// DeviceListEvent is sent when the set of visible devices changes (a device
// appears, disappears, or its properties change). Subscribers receive a
// snapshot of the full device list at the time of the change plus the update
// reason.
type DeviceListEvent struct {
	// Devices is the current device list snapshot.
	Devices []DeviceInfo

	// DevicesThatChanged contains the IDs of devices that triggered this update.
	DevicesThatChanged []string

	// Reason is the cluster update reason (e.g. NEW_DEVICE_APPEARED,
	// DEVICES_DISAPPEARED, DEVICE_STATE_CHANGED).
	Reason string
}

// PlaybackEvent is sent when the playback state changes (play, pause, seek,
// track change, shuffle/repeat toggle, etc.).
type PlaybackEvent struct {
	// State is the current player state snapshot.
	State PlayerState
}

// MetadataEvent is sent when the currently playing track changes and rich
// metadata has been fetched from the private metadata API. This includes
// the track title, artist, album, cover art URL, and duration — fields that
// are NOT available in the connect-state cluster push alone.
type MetadataEvent struct {
	// Metadata is the enriched track metadata.
	Metadata TrackMetadata
}

// ---------------------------------------------------------------------------
// Channel subscriptions
// ---------------------------------------------------------------------------

const (
	// defaultEventChanSize is the buffer size for event channels. A small
	// buffer prevents blocking the cluster update goroutine when consumers
	// are slightly slow.
	defaultEventChanSize = 16
)

// eventSubscribers holds all active event channel subscriptions.
type eventSubscribers struct {
	mu sync.RWMutex

	deviceList []chan DeviceListEvent
	playback   []chan PlaybackEvent
	metadata   []chan MetadataEvent
}

// SubscribeDeviceList returns a buffered channel that receives DeviceListEvent
// values whenever the visible device list changes. The channel is closed when
// the Controller is closed. Multiple subscriptions are supported.
func (c *Controller) SubscribeDeviceList() <-chan DeviceListEvent {
	ch := make(chan DeviceListEvent, defaultEventChanSize)
	c.subs.mu.Lock()
	c.subs.deviceList = append(c.subs.deviceList, ch)
	c.subs.mu.Unlock()
	return ch
}

// SubscribePlayback returns a buffered channel that receives PlaybackEvent
// values whenever the playback state changes (play/pause, track change, seek,
// shuffle/repeat change, etc.). The channel is closed when the Controller is
// closed.
func (c *Controller) SubscribePlayback() <-chan PlaybackEvent {
	ch := make(chan PlaybackEvent, defaultEventChanSize)
	c.subs.mu.Lock()
	c.subs.playback = append(c.subs.playback, ch)
	c.subs.mu.Unlock()
	return ch
}

// SubscribeMetadata returns a buffered channel that receives MetadataEvent
// values whenever the currently playing track changes AND its rich metadata
// has been fetched from the private metadata API. The channel is closed when
// the Controller is closed. Metadata is fetched asynchronously in the
// background; the event is delivered once the fetch completes.
func (c *Controller) SubscribeMetadata() <-chan MetadataEvent {
	ch := make(chan MetadataEvent, defaultEventChanSize)
	c.subs.mu.Lock()
	c.subs.metadata = append(c.subs.metadata, ch)
	c.subs.mu.Unlock()
	return ch
}

// ---------------------------------------------------------------------------
// Event dispatch helpers (called from cluster update processing)
// ---------------------------------------------------------------------------

// emitDeviceList sends a DeviceListEvent to all subscribers.
func (c *Controller) emitDeviceList(evt DeviceListEvent) {
	c.subs.mu.RLock()
	defer c.subs.mu.RUnlock()
	for _, ch := range c.subs.deviceList {
		select {
		case ch <- evt:
		default:
			c.log.Debugf("dropping DeviceListEvent: subscriber channel full")
		}
	}
}

// emitPlayback sends a PlaybackEvent to all subscribers.
func (c *Controller) emitPlayback(evt PlaybackEvent) {
	c.subs.mu.RLock()
	defer c.subs.mu.RUnlock()
	for _, ch := range c.subs.playback {
		select {
		case ch <- evt:
		default:
			c.log.Debugf("dropping PlaybackEvent: subscriber channel full")
		}
	}
}

// emitMetadata sends a MetadataEvent to all subscribers.
func (c *Controller) emitMetadata(evt MetadataEvent) {
	c.subs.mu.RLock()
	defer c.subs.mu.RUnlock()
	for _, ch := range c.subs.metadata {
		select {
		case ch <- evt:
		default:
			c.log.Debugf("dropping MetadataEvent: subscriber channel full")
		}
	}
}

// closeAllSubscribers closes all event channels. Called from Controller.Close().
func (c *Controller) closeAllSubscribers() {
	c.subs.mu.Lock()
	defer c.subs.mu.Unlock()
	for _, ch := range c.subs.deviceList {
		close(ch)
	}
	c.subs.deviceList = nil
	for _, ch := range c.subs.playback {
		close(ch)
	}
	c.subs.playback = nil
	for _, ch := range c.subs.metadata {
		close(ch)
	}
	c.subs.metadata = nil
}

// ---------------------------------------------------------------------------
// Cluster update diffing & event emission
// ---------------------------------------------------------------------------

// processClusterUpdate is called every time a new cluster update is received.
// It compares the new cluster to the previous one and emits the appropriate
// events on subscriber channels.
func (c *Controller) processClusterUpdate(newCluster *connectpb.Cluster, updateReason connectpb.ClusterUpdateReason, devicesThatChanged []string) {
	// ---- Device list change detection ----
	c.emitDeviceListFromCluster(newCluster, updateReason, devicesThatChanged)

	// ---- Playback state change detection ----
	newPS := buildPlayerState(newCluster)
	if newPS != nil {
		c.emitPlayback(PlaybackEvent{State: *newPS})
	}

	// ---- Track change → metadata fetch ----
	newTrackURI := ""
	if ps := newCluster.GetPlayerState(); ps != nil {
		if t := ps.GetTrack(); t != nil {
			newTrackURI = t.GetUri()
		}
	}

	c.lastTrackMu.Lock()
	trackChanged := newTrackURI != "" && newTrackURI != c.lastTrackURI
	if trackChanged {
		c.lastTrackURI = newTrackURI
	}
	c.lastTrackMu.Unlock()

	if trackChanged {
		c.fetchAndEmitMetadata(newTrackURI, newCluster)
	}
}

// emitDeviceListFromCluster builds a DeviceListEvent from the cluster and
// emits it.
func (c *Controller) emitDeviceListFromCluster(cluster *connectpb.Cluster, reason connectpb.ClusterUpdateReason, changed []string) {
	if cluster == nil {
		return
	}

	activeId := cluster.GetActiveDeviceId()
	devices := make([]DeviceInfo, 0, len(cluster.GetDevice()))
	for id, info := range cluster.GetDevice() {
		di := DeviceInfo{
			Id:             id,
			Name:           info.GetName(),
			Type:           info.GetDeviceType().String(),
			IsActive:       id == activeId,
			Volume:         int(info.GetVolume()) * 100 / 65535,
			SupportsVolume: info.GetCapabilities() != nil && !info.GetCapabilities().GetDisableVolume(),
		}
		devices = append(devices, di)
	}

	c.emitDeviceList(DeviceListEvent{
		Devices:            devices,
		DevicesThatChanged: changed,
		Reason:             reason.String(),
	})
}

// buildPlayerState creates a PlayerState from a cluster snapshot. Returns nil
// if the cluster or its player state is nil.
func buildPlayerState(cluster *connectpb.Cluster) *PlayerState {
	if cluster == nil {
		return nil
	}
	ps := cluster.GetPlayerState()
	if ps == nil {
		return nil
	}

	isPlaying := ps.GetIsPlaying() && !ps.GetIsPaused()

	positionMs := ps.GetPositionAsOfTimestamp()
	if isPlaying && ps.GetTimestamp() > 0 {
		elapsed := time.Now().UnixMilli() - ps.GetTimestamp()
		if elapsed > 0 {
			speed := ps.GetPlaybackSpeed()
			if speed <= 0 {
				speed = 1.0
			}
			positionMs += int64(float64(elapsed) * speed)
		}
		dur := ps.GetDuration()
		if dur > 0 && positionMs > dur {
			positionMs = dur
		}
	}

	trackURI := ""
	if t := ps.GetTrack(); t != nil {
		trackURI = t.GetUri()
	}

	var shuffle, repeatCtx, repeatTrack bool
	if opts := ps.GetOptions(); opts != nil {
		shuffle = opts.GetShufflingContext()
		repeatCtx = opts.GetRepeatingContext()
		repeatTrack = opts.GetRepeatingTrack()
	}

	return &PlayerState{
		IsPlaying:     isPlaying,
		TrackURI:      trackURI,
		ContextURI:    ps.GetContextUri(),
		PositionMs:    positionMs,
		DurationMs:    ps.GetDuration(),
		DeviceId:      cluster.GetActiveDeviceId(),
		Shuffle:       shuffle,
		RepeatContext: repeatCtx,
		RepeatTrack:   repeatTrack,
	}
}

// ---------------------------------------------------------------------------
// Metadata fetching
// ---------------------------------------------------------------------------

// trackURIToHex extracts the hex GID from a Spotify track URI. For example,
// "spotify:track:5TFCp6cxCaJRhbdn6IWEGh" → "c1c98828fc2c44d6bb247ad01bdb7d4d".
func trackURIToHex(uri string) (string, error) {
	sid, err := spotcontrol.SpotifyIdFromUri(uri)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sid.Id()), nil
}

// fetchAndEmitMetadata fetches rich track metadata from the private spclient
// API in a background goroutine and emits a MetadataEvent when done.
func (c *Controller) fetchAndEmitMetadata(trackURI string, cluster *connectpb.Cluster) {
	go func() {
		hexId, err := trackURIToHex(trackURI)
		if err != nil {
			c.log.Warnf("cannot convert track URI %q to hex for metadata fetch: %v", trackURI, err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		raw, err := c.sp.GetTrackMetadata(ctx, hexId)
		if err != nil {
			c.log.WithError(err).Warnf("failed fetching track metadata for %s", trackURI)
			return
		}

		meta := TrackMetadata{
			TrackURI:      trackURI,
			Title:         raw.Name,
			Artist:        raw.ArtistName(),
			Album:         raw.AlbumName(),
			DurationMs:    raw.Duration,
			ImageURL:      raw.LargeImageURL(),
			SmallImageURL: raw.SmallImageURL(),
			Raw:           raw,
		}

		// Populate artist/album URIs from the cluster's ProvidedTrack if
		// available (they are not in the metadata API response).
		if cluster != nil {
			if ps := cluster.GetPlayerState(); ps != nil {
				if t := ps.GetTrack(); t != nil {
					meta.ArtistURI = t.GetArtistUri()
					meta.AlbumURI = t.GetAlbumUri()
				}
			}
		}

		c.log.Debugf("fetched metadata for %s: %q by %q on %q", trackURI, meta.Title, meta.Artist, meta.Album)

		// Cache the metadata.
		c.lastMetaMu.Lock()
		c.lastMeta = &meta
		c.lastMetaMu.Unlock()

		c.emitMetadata(MetadataEvent{Metadata: meta})
	}()
}

// ---------------------------------------------------------------------------
// Public metadata accessors
// ---------------------------------------------------------------------------

// GetTrackMetadata returns the cached rich metadata for the currently playing
// track, or nil if no track is playing or metadata has not been fetched yet.
// This is a non-blocking, instant read from the cache populated by the
// background metadata fetch triggered on each track change.
func (c *Controller) GetTrackMetadata() *TrackMetadata {
	c.lastMetaMu.RLock()
	defer c.lastMetaMu.RUnlock()
	if c.lastMeta == nil {
		return nil
	}
	meta := *c.lastMeta
	return &meta
}

// FetchTrackMetadata fetches rich metadata for the given track URI from the
// private spclient metadata API. Unlike GetTrackMetadata (which returns the
// cached value for the current track), this method makes a network request
// and can be used for any track URI.
//
// The trackURI should be a Spotify track URI, e.g. "spotify:track:5TFCp6cxCaJRhbdn6IWEGh".
func (c *Controller) FetchTrackMetadata(ctx context.Context, trackURI string) (*TrackMetadata, error) {
	hexId, err := trackURIToHex(trackURI)
	if err != nil {
		return nil, fmt.Errorf("invalid track URI %q: %w", trackURI, err)
	}

	raw, err := c.sp.GetTrackMetadata(ctx, hexId)
	if err != nil {
		return nil, err
	}

	meta := &TrackMetadata{
		TrackURI:      trackURI,
		Title:         raw.Name,
		Artist:        raw.ArtistName(),
		Album:         raw.AlbumName(),
		DurationMs:    raw.Duration,
		ImageURL:      raw.LargeImageURL(),
		SmallImageURL: raw.SmallImageURL(),
		Raw:           raw,
	}

	return meta, nil
}

// FetchCurrentTrackMetadata fetches rich metadata for the currently playing
// track from the private spclient metadata API. This always makes a network
// request (it does not use the cache). Returns nil with no error if no track
// is currently playing.
func (c *Controller) FetchCurrentTrackMetadata(ctx context.Context) (*TrackMetadata, error) {
	c.clusterLock.RLock()
	cluster := c.cluster
	c.clusterLock.RUnlock()

	if cluster == nil {
		return nil, nil
	}
	ps := cluster.GetPlayerState()
	if ps == nil {
		return nil, nil
	}
	t := ps.GetTrack()
	if t == nil || t.GetUri() == "" {
		return nil, nil
	}

	trackURI := t.GetUri()
	if !strings.HasPrefix(trackURI, "spotify:track:") {
		return nil, fmt.Errorf("current track is not a track URI (got %q)", trackURI)
	}

	meta, err := c.FetchTrackMetadata(ctx, trackURI)
	if err != nil {
		return nil, err
	}

	// Populate artist/album URIs from the cluster's ProvidedTrack.
	meta.ArtistURI = t.GetArtistUri()
	meta.AlbumURI = t.GetAlbumUri()

	return meta, nil
}
