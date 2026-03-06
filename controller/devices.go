package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

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
// requires a network request and is subject to rate limits.
//
// If a cached cluster is available, this method returns devices from the
// cluster instead to avoid unnecessary API calls. Pass forceAPI=true via
// ListDevicesFromAPIForced to bypass the cluster cache.
func (c *Controller) ListDevicesFromAPI(ctx context.Context) ([]DeviceInfo, error) {
	// Prefer cluster data when available to avoid hitting Web API rate limits.
	if devices := c.ListDevices(); len(devices) > 0 {
		return devices, nil
	}

	return c.ListDevicesFromAPIForced(ctx)
}

// ListDevicesFromAPIForced always queries the Spotify Web API for devices,
// bypassing the cluster cache. Use ListDevicesFromAPI for the recommended
// cluster-first approach.
func (c *Controller) ListDevicesFromAPIForced(ctx context.Context) ([]DeviceInfo, error) {
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
