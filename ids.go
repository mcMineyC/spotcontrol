package spotcontrol

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// UriRegexp matches Spotify URIs in the form spotify:type:id.
var UriRegexp = regexp.MustCompile(`^spotify:([a-z]+):([0-9a-zA-Z]{21,22})$`)

const base62Alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// SpotifyIdType represents the type component of a Spotify URI.
type SpotifyIdType string

const (
	SpotifyIdTypeTrack    SpotifyIdType = "track"
	SpotifyIdTypeEpisode  SpotifyIdType = "episode"
	SpotifyIdTypeAlbum    SpotifyIdType = "album"
	SpotifyIdTypeArtist   SpotifyIdType = "artist"
	SpotifyIdTypePlaylist SpotifyIdType = "playlist"
	SpotifyIdTypeShow     SpotifyIdType = "show"
)

// SpotifyId represents a Spotify resource identifier, consisting of a type and a 16-byte ID.
type SpotifyId struct {
	typ SpotifyIdType
	id  []byte
}

// Type returns the type of this Spotify ID (e.g., "track", "episode").
func (sid SpotifyId) Type() SpotifyIdType {
	return sid.typ
}

// Id returns the raw 16-byte identifier.
func (sid SpotifyId) Id() []byte {
	return sid.id
}

// Hex returns the hex-encoded string of the raw identifier.
func (sid SpotifyId) Hex() string {
	return fmt.Sprintf("%x", sid.id)
}

// Base62 returns the base62-encoded string of the raw identifier, zero-padded to 22 characters.
func (sid SpotifyId) Base62() string {
	return GidToBase62(sid.id)
}

// Uri returns the full Spotify URI, e.g., "spotify:track:6rqhFgbbKwnb9MLmUQDhG6".
func (sid SpotifyId) Uri() string {
	return fmt.Sprintf("spotify:%s:%s", sid.Type(), sid.Base62())
}

// String returns the URI representation of this SpotifyId.
func (sid SpotifyId) String() string {
	return sid.Uri()
}

// GidToBase62 converts a raw 16-byte GID to a base62-encoded string, zero-padded to 22 characters.
func GidToBase62(id []byte) string {
	s := new(big.Int).SetBytes(id).Text(62)
	if len(s) < 22 {
		s = strings.Repeat("0", 22-len(s)) + s
	}
	return s
}

// Base62ToGid converts a base62-encoded string to a 16-byte GID.
func Base62ToGid(id string) ([]byte, error) {
	var i big.Int
	_, ok := i.SetString(id, 62)
	if !ok {
		return nil, fmt.Errorf("failed decoding base62: %s", id)
	}
	return i.FillBytes(make([]byte, 16)), nil
}

// Convert62 converts a base62-encoded Spotify ID string to raw bytes.
// This is a legacy helper; prefer Base62ToGid for new code.
func Convert62(id string) []byte {
	base := big.NewInt(62)
	n := &big.Int{}
	for _, c := range []byte(id) {
		d := big.NewInt(int64(strings.IndexByte(base62Alphabet, c)))
		n = n.Mul(n, base)
		n = n.Add(n, d)
	}
	return n.Bytes()
}

// ConvertTo62 converts raw bytes to a base62-encoded Spotify ID string, zero-padded to 22 characters.
// This is a legacy helper; prefer GidToBase62 for new code.
func ConvertTo62(raw []byte) string {
	bi := new(big.Int).SetBytes(raw)
	rem := big.NewInt(0)
	base := big.NewInt(62)
	zero := big.NewInt(0)
	result := ""

	for bi.Cmp(zero) > 0 {
		bi, rem = bi.DivMod(bi, base, rem)
		result += string(base62Alphabet[int(rem.Uint64())])
	}

	for len(result) < 22 {
		result += "0"
	}

	// reverse
	r := []rune(result)
	for i, j := 0, len(r)-1; i < len(r)/2; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// SpotifyIdFromGid creates a SpotifyId from a type and raw 16-byte GID.
func SpotifyIdFromGid(typ SpotifyIdType, id []byte) SpotifyId {
	if len(id) != 16 {
		panic(fmt.Sprintf("invalid gid length %d: %x", len(id), id))
	}
	return SpotifyId{typ: typ, id: id}
}

// SpotifyIdFromBase62 creates a SpotifyId from a type and base62-encoded string.
func SpotifyIdFromBase62(typ SpotifyIdType, id string) (*SpotifyId, error) {
	var i big.Int
	_, ok := i.SetString(id, 62)
	if !ok {
		return nil, fmt.Errorf("failed decoding base62: %s", id)
	}
	return &SpotifyId{typ: typ, id: i.FillBytes(make([]byte, 16))}, nil
}

// SpotifyIdFromUri parses a Spotify URI (e.g., "spotify:track:6rqhFgbbKwnb9MLmUQDhG6")
// and returns the corresponding SpotifyId.
func SpotifyIdFromUri(uri string) (*SpotifyId, error) {
	matches := UriRegexp.FindStringSubmatch(uri)
	if len(matches) == 0 {
		return nil, fmt.Errorf("invalid uri: %s", uri)
	}
	return SpotifyIdFromBase62(SpotifyIdType(matches[1]), matches[2])
}

// InferSpotifyIdTypeFromContextUri determines whether a context URI refers
// to episode/show content or track content.
func InferSpotifyIdTypeFromContextUri(uri string) SpotifyIdType {
	if strings.HasPrefix(uri, "spotify:episode:") || strings.HasPrefix(uri, "spotify:show:") {
		return SpotifyIdTypeEpisode
	}
	return SpotifyIdTypeTrack
}
