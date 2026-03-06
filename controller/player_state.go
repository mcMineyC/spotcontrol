package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// ---------------------------------------------------------------------------
// Player state
// ---------------------------------------------------------------------------

// GetPlayerStateFromCluster extracts the current playback state from the
// cached connect-state cluster. This is instantaneous, requires no network
// request, and is not subject to rate limits.
//
// Returns nil if no cluster data is available yet or if there is no active
// playback.
func (c *Controller) GetPlayerStateFromCluster() *PlayerState {
	c.clusterLock.RLock()
	cluster := c.cluster
	c.clusterLock.RUnlock()

	if cluster == nil {
		return nil
	}

	ps := cluster.GetPlayerState()
	if ps == nil {
		return nil
	}

	// Determine if actively playing: IsPlaying && !IsPaused.
	isPlaying := ps.GetIsPlaying() && !ps.GetIsPaused()

	// Compute the estimated current position. The cluster stores the position
	// as-of a timestamp; if playing, we advance by the elapsed wall-clock time
	// scaled by playback speed.
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

		// Clamp to duration.
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

// GetPlayerState returns the current playback state. It first attempts to read
// from the cached connect-state cluster (instantaneous, no rate limit). If the
// cluster is not available, it falls back to querying the Spotify Web API.
func (c *Controller) GetPlayerState(ctx context.Context) (*PlayerState, error) {
	// Try cluster first.
	if ps := c.GetPlayerStateFromCluster(); ps != nil {
		return ps, nil
	}

	// Fall back to Web API.
	return c.GetPlayerStateFromAPI(ctx)
}

// GetPlayerStateFromAPI queries the Spotify Web API for the current playback
// state. This is subject to rate limits; prefer GetPlayerState which uses the
// cluster cache when available.
func (c *Controller) GetPlayerStateFromAPI(ctx context.Context) (*PlayerState, error) {
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
