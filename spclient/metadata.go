package spclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ---------------------------------------------------------------------------
// Private metadata API types
// ---------------------------------------------------------------------------

// TrackMetadata represents the JSON response from the private spclient
// metadata endpoint:
//
//	GET /metadata/4/track/{hex_id}?market=from_token
//
// This endpoint returns detailed track information including album art, artist
// names, duration, and more. It uses the Login5 bearer token (same as other
// spclient endpoints) and does NOT require an OAuth2 Web API token.
//
// The structure was determined by capturing traffic from the Spotify desktop
// client via mitmproxy (see cuts/metadata_track.txt).
type TrackMetadata struct {
	Gid          string                `json:"gid"`
	Name         string                `json:"name"`
	Album        *TrackMetadataAlbum   `json:"album,omitempty"`
	Artist       []TrackMetadataArtist `json:"artist,omitempty"`
	Number       int                   `json:"number,omitempty"`
	DiscNumber   int                   `json:"disc_number,omitempty"`
	Duration     int64                 `json:"duration"`
	Popularity   int                   `json:"popularity,omitempty"`
	CanonicalUri string                `json:"canonical_uri,omitempty"`
	ExternalId   []TrackExternalId     `json:"external_id,omitempty"`
	MediaType    string                `json:"media_type,omitempty"`
}

// TrackMetadataAlbum is the album object nested in a track metadata response.
type TrackMetadataAlbum struct {
	Gid        string                `json:"gid"`
	Name       string                `json:"name"`
	Artist     []TrackMetadataArtist `json:"artist,omitempty"`
	Label      string                `json:"label,omitempty"`
	Date       *TrackMetadataDate    `json:"date,omitempty"`
	CoverGroup *TrackCoverGroup      `json:"cover_group,omitempty"`
}

// TrackMetadataArtist is an artist object in the metadata response.
type TrackMetadataArtist struct {
	Gid  string `json:"gid"`
	Name string `json:"name"`
}

// TrackMetadataDate is a release date in the metadata response.
type TrackMetadataDate struct {
	Year  int `json:"year"`
	Month int `json:"month,omitempty"`
	Day   int `json:"day,omitempty"`
}

// TrackCoverGroup contains album cover images at various sizes.
type TrackCoverGroup struct {
	Image []TrackCoverImage `json:"image,omitempty"`
}

// TrackCoverImage is a single album cover image.
type TrackCoverImage struct {
	FileId string `json:"file_id"`
	Size   string `json:"size"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// TrackExternalId is an external identifier (e.g. ISRC) for a track.
type TrackExternalId struct {
	Type string `json:"type"`
	Id   string `json:"id"`
}

// ImageURL returns the URL for the cover image of the given size. Valid size
// values are "SMALL" (64px), "DEFAULT" (300px), and "LARGE" (640px). If the
// requested size is not found, the largest available image is returned. If no
// images are available, an empty string is returned.
//
// Spotify serves cover art from https://i.scdn.co/image/{file_id}.
func (m *TrackMetadata) ImageURL(size string) string {
	if m.Album == nil || m.Album.CoverGroup == nil || len(m.Album.CoverGroup.Image) == 0 {
		return ""
	}

	images := m.Album.CoverGroup.Image

	// Try to find the requested size.
	for _, img := range images {
		if img.Size == size {
			return "https://i.scdn.co/image/" + img.FileId
		}
	}

	// Fallback: return the largest image (last in the list, which is
	// typically LARGE based on the observed API response ordering).
	var best *TrackCoverImage
	for i := range images {
		if best == nil || images[i].Width > best.Width {
			best = &images[i]
		}
	}
	if best != nil {
		return "https://i.scdn.co/image/" + best.FileId
	}

	return ""
}

// LargeImageURL is a convenience method that returns the LARGE (640px) cover
// image URL, falling back to any available image.
func (m *TrackMetadata) LargeImageURL() string {
	return m.ImageURL("LARGE")
}

// DefaultImageURL is a convenience method that returns the DEFAULT (300px)
// cover image URL, falling back to any available image.
func (m *TrackMetadata) DefaultImageURL() string {
	return m.ImageURL("DEFAULT")
}

// SmallImageURL is a convenience method that returns the SMALL (64px) cover
// image URL, falling back to any available image.
func (m *TrackMetadata) SmallImageURL() string {
	return m.ImageURL("SMALL")
}

// ArtistName returns the name of the first (primary) artist, or an empty
// string if no artists are present.
func (m *TrackMetadata) ArtistName() string {
	if len(m.Artist) > 0 {
		return m.Artist[0].Name
	}
	return ""
}

// AlbumName returns the album name, or an empty string if no album info is
// present.
func (m *TrackMetadata) AlbumName() string {
	if m.Album != nil {
		return m.Album.Name
	}
	return ""
}

// ---------------------------------------------------------------------------
// Spclient method
// ---------------------------------------------------------------------------

// GetTrackMetadata fetches detailed metadata for a track from the private
// spclient metadata API. The trackHexId is the 32-character hex-encoded track
// GID (e.g. "c1c98828fc2c44d6bb247ad01bdb7d4d").
//
// This uses the endpoint:
//
//	GET /metadata/4/track/{hex_id}?market=from_token
//
// which returns a JSON response with the track name, album (including cover
// art), artists, duration, and other metadata. It requires only the Login5
// bearer token — no OAuth2 Web API token is needed.
//
// The response is cached by the Spotify CDN (typically with a long max-age),
// so repeated calls for the same track are inexpensive.
func (c *Spclient) GetTrackMetadata(ctx context.Context, trackHexId string) (*TrackMetadata, error) {
	path := fmt.Sprintf("/metadata/4/track/%s", trackHexId)

	resp, err := c.Request(ctx, "GET", path, map[string][]string{
		"market": {"from_token"},
	}, http.Header{
		"Accept": {"application/json"},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed fetching track metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("track metadata request returned status %d: %s", resp.StatusCode, string(body))
	}

	var meta TrackMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("failed decoding track metadata response: %w", err)
	}

	return &meta, nil
}
