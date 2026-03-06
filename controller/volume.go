package controller

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// ---------------------------------------------------------------------------
// Volume control
// ---------------------------------------------------------------------------

// SetVolume sets the playback volume. The volume is in the range 0-65535 when
// using the connect-state protocol (matching librespot's internal volume
// representation), or 0-100 when using the Web API fallback.
//
// Volume updates are debounced (500ms by default, matching librespot's
// VOLUME_UPDATE_DELAY) so rapid adjustments don't flood the backend.
//
// For the connect-state path, volume is sent via the dedicated volume
// signaling endpoint (PUT /connect-state/v1/connect/volume/from/{from}/to/{to})
// using a SetVolumeCommand protobuf, which is the same mechanism librespot uses.
//
// When UseWebApi is set, volume is sent via the public Web API instead.
func (c *Controller) SetVolume(ctx context.Context, volumePercent int, deviceId string) error {
	if c.useWebApi {
		return c.setVolumeWebApi(ctx, volumePercent, deviceId)
	}

	// Resolve the target device for connect-state volume endpoint.
	target := deviceId
	if target == "" {
		target = c.ActiveDeviceId()
	}
	if target == "" {
		// Fall back to the Web API path if we can't determine a target device
		// for the connect-state volume endpoint.
		return c.setVolumeWebApi(ctx, volumePercent, deviceId)
	}

	// Convert percentage (0-100) to connect-state volume (0-65535).
	volume := int32(volumePercent) * 65535 / 100
	if volume < 0 {
		volume = 0
	}
	if volume > 65535 {
		volume = 65535
	}

	// Debounce: record the latest volume and reset the timer.
	if c.volumeDebounceDur > 0 {
		c.volumeMu.Lock()
		c.pendingVolume = int(volume)
		c.pendingVolumeId = target

		if c.volumeTimer == nil {
			c.volumeTimer = time.AfterFunc(c.volumeDebounceDur, func() {
				c.flushVolume()
			})
		} else {
			c.volumeTimer.Reset(c.volumeDebounceDur)
		}
		c.volumeDebouncing = true
		c.volumeMu.Unlock()

		c.log.Debugf("volume update debounced: %d -> device %s", volume, target)
		return nil
	}

	// No debouncing: send immediately.
	return c.sendVolumeNow(ctx, target, volume)
}

// flushVolume is called by the debounce timer to send the most recent pending
// volume command.
func (c *Controller) flushVolume() {
	c.volumeMu.Lock()
	vol := c.pendingVolume
	target := c.pendingVolumeId
	c.volumeDebouncing = false
	c.volumeMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c.log.Debugf("flushing debounced volume: %d -> device %s", vol, target)
	if err := c.sendVolumeNow(ctx, target, int32(vol)); err != nil {
		c.log.WithError(err).Errorf("failed sending debounced volume command")
	}
}

// sendVolumeNow sends a volume command immediately via the connect-state
// volume endpoint.
func (c *Controller) sendVolumeNow(ctx context.Context, targetDeviceId string, volume int32) error {
	connId := c.connectionId()
	if connId == "" {
		return fmt.Errorf("no connection ID available; is the dealer connected?")
	}

	return c.sp.ConnectSetVolume(ctx, connId, targetDeviceId, volume)
}

func (c *Controller) setVolumeWebApi(ctx context.Context, volumePercent int, deviceId string) error {
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

	resp, err := c.playerRequest(ctx, "PUT", "/v1/me/player/volume", query, nil, nil)
	if err != nil {
		return fmt.Errorf("failed sending volume command: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return checkPlayerResponse(resp)
}
