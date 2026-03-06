package spotcontrol

import (
	"bytes"
	"testing"
)

func TestGidToBase62AndBack(t *testing.T) {
	// A well-known Spotify track GID (16 bytes).
	gid := []byte{0x6b, 0x7e, 0x3e, 0x01, 0x31, 0xd8, 0x42, 0x21, 0xa7, 0x30, 0x25, 0x61, 0xcc, 0x12, 0x60, 0xb5}

	base62 := GidToBase62(gid)
	if len(base62) != 22 {
		t.Fatalf("GidToBase62 returned string of length %d, want 22", len(base62))
	}

	roundTrip, err := Base62ToGid(base62)
	if err != nil {
		t.Fatalf("Base62ToGid(%q) error: %v", base62, err)
	}

	if !bytes.Equal(gid, roundTrip) {
		t.Errorf("round-trip failed: original=%x, got=%x", gid, roundTrip)
	}
}

func TestBase62ToGidAndBack(t *testing.T) {
	base62 := "3Vn9oCZbdI1EMO7jxdz2Rc"

	gid, err := Base62ToGid(base62)
	if err != nil {
		t.Fatalf("Base62ToGid(%q) error: %v", base62, err)
	}

	if len(gid) != 16 {
		t.Fatalf("Base62ToGid returned %d bytes, want 16", len(gid))
	}

	back := GidToBase62(gid)
	if back != base62 {
		t.Errorf("round-trip failed: original=%q, got=%q", base62, back)
	}
}

func TestGidToBase62ZeroPadding(t *testing.T) {
	// A GID that starts with zeros should still produce a 22-char base62 string.
	gid := make([]byte, 16)
	gid[15] = 1

	base62 := GidToBase62(gid)
	if len(base62) != 22 {
		t.Fatalf("GidToBase62 returned string of length %d, want 22", len(base62))
	}

	roundTrip, err := Base62ToGid(base62)
	if err != nil {
		t.Fatalf("Base62ToGid(%q) error: %v", base62, err)
	}

	if !bytes.Equal(gid, roundTrip) {
		t.Errorf("round-trip with leading zeros failed: original=%x, got=%x", gid, roundTrip)
	}
}

func TestBase62ToGidInvalid(t *testing.T) {
	// Characters outside the base62 alphabet should fail.
	_, err := Base62ToGid("!!!invalid!!!")
	if err == nil {
		t.Fatal("expected error for invalid base62 string, got nil")
	}
}

func TestConvert62Legacy(t *testing.T) {
	id := "3Vn9oCZbdI1EMO7jxdz2Rc"
	raw := Convert62(id)
	if len(raw) == 0 {
		t.Fatal("Convert62 returned empty result")
	}

	back := ConvertTo62(raw)
	if back != id {
		t.Errorf("ConvertTo62(Convert62(%q)) = %q, want %q", id, back, id)
	}
}

func TestConvertTo62Legacy(t *testing.T) {
	gid := []byte{0x6b, 0x7e, 0x3e, 0x01, 0x31, 0xd8, 0x42, 0x21, 0xa7, 0x30, 0x25, 0x61, 0xcc, 0x12, 0x60, 0xb5}

	base62 := ConvertTo62(gid)
	if len(base62) != 22 {
		t.Fatalf("ConvertTo62 returned string of length %d, want 22", len(base62))
	}

	// The modern function should produce the same result.
	modern := GidToBase62(gid)
	if base62 != modern {
		t.Errorf("ConvertTo62 = %q, GidToBase62 = %q, want same", base62, modern)
	}
}

func TestConvert62LegacyMatchesModern(t *testing.T) {
	id := "3Vn9oCZbdI1EMO7jxdz2Rc"
	legacy := Convert62(id)
	modern, err := Base62ToGid(id)
	if err != nil {
		t.Fatalf("Base62ToGid error: %v", err)
	}

	if !bytes.Equal(legacy, modern) {
		t.Errorf("Convert62 = %x, Base62ToGid = %x, want same", legacy, modern)
	}
}

func TestSpotifyIdFromUri(t *testing.T) {
	tests := []struct {
		uri     string
		wantTyp SpotifyIdType
		wantErr bool
	}{
		{
			uri:     "spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
			wantTyp: SpotifyIdTypeTrack,
		},
		{
			uri:     "spotify:episode:3Vn9oCZbdI1EMO7jxdz2Rc",
			wantTyp: SpotifyIdTypeEpisode,
		},
		{
			uri:     "spotify:album:6rqhFgbbKwnb9MLmUQDhG6",
			wantTyp: SpotifyIdTypeAlbum,
		},
		{
			uri:     "spotify:artist:6rqhFgbbKwnb9MLmUQDhG6",
			wantTyp: SpotifyIdTypeArtist,
		},
		{
			uri:     "spotify:playlist:6rqhFgbbKwnb9MLmUQDhG6",
			wantTyp: SpotifyIdTypePlaylist,
		},
		{
			uri:     "invalid-uri",
			wantErr: true,
		},
		{
			uri:     "",
			wantErr: true,
		},
		{
			uri:     "spotify:track:short",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			sid, err := SpotifyIdFromUri(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for URI %q, got nil", tt.uri)
				}
				return
			}
			if err != nil {
				t.Fatalf("SpotifyIdFromUri(%q) error: %v", tt.uri, err)
			}
			if sid.Type() != tt.wantTyp {
				t.Errorf("type = %q, want %q", sid.Type(), tt.wantTyp)
			}
			if len(sid.Id()) != 16 {
				t.Errorf("id length = %d, want 16", len(sid.Id()))
			}
		})
	}
}

func TestSpotifyIdRoundTrip(t *testing.T) {
	uri := "spotify:track:6rqhFgbbKwnb9MLmUQDhG6"

	sid, err := SpotifyIdFromUri(uri)
	if err != nil {
		t.Fatalf("SpotifyIdFromUri(%q) error: %v", uri, err)
	}

	if sid.Uri() != uri {
		t.Errorf("Uri() = %q, want %q", sid.Uri(), uri)
	}

	if sid.String() != uri {
		t.Errorf("String() = %q, want %q", sid.String(), uri)
	}
}

func TestSpotifyIdFromGid(t *testing.T) {
	gid := []byte{0x6b, 0x7e, 0x3e, 0x01, 0x31, 0xd8, 0x42, 0x21, 0xa7, 0x30, 0x25, 0x61, 0xcc, 0x12, 0x60, 0xb5}

	sid := SpotifyIdFromGid(SpotifyIdTypeTrack, gid)

	if sid.Type() != SpotifyIdTypeTrack {
		t.Errorf("type = %q, want %q", sid.Type(), SpotifyIdTypeTrack)
	}

	if !bytes.Equal(sid.Id(), gid) {
		t.Errorf("id = %x, want %x", sid.Id(), gid)
	}

	// Base62 should be 22 chars.
	if len(sid.Base62()) != 22 {
		t.Errorf("Base62 length = %d, want 22", len(sid.Base62()))
	}

	// Hex should be 32 chars.
	if len(sid.Hex()) != 32 {
		t.Errorf("Hex length = %d, want 32", len(sid.Hex()))
	}
}

func TestSpotifyIdFromGidPanicsOnBadLength(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid gid length")
		}
	}()

	SpotifyIdFromGid(SpotifyIdTypeTrack, []byte{0x01, 0x02, 0x03})
}

func TestSpotifyIdFromBase62(t *testing.T) {
	sid, err := SpotifyIdFromBase62(SpotifyIdTypeTrack, "6rqhFgbbKwnb9MLmUQDhG6")
	if err != nil {
		t.Fatalf("SpotifyIdFromBase62 error: %v", err)
	}

	if sid.Type() != SpotifyIdTypeTrack {
		t.Errorf("type = %q, want %q", sid.Type(), SpotifyIdTypeTrack)
	}

	if sid.Base62() != "6rqhFgbbKwnb9MLmUQDhG6" {
		t.Errorf("Base62() = %q, want %q", sid.Base62(), "6rqhFgbbKwnb9MLmUQDhG6")
	}
}

func TestSpotifyIdFromBase62Invalid(t *testing.T) {
	_, err := SpotifyIdFromBase62(SpotifyIdTypeTrack, "!!invalid!!")
	if err == nil {
		t.Fatal("expected error for invalid base62, got nil")
	}
}

func TestInferSpotifyIdTypeFromContextUri(t *testing.T) {
	tests := []struct {
		uri  string
		want SpotifyIdType
	}{
		{"spotify:episode:abc123", SpotifyIdTypeEpisode},
		{"spotify:show:abc123", SpotifyIdTypeEpisode},
		{"spotify:track:abc123", SpotifyIdTypeTrack},
		{"spotify:album:abc123", SpotifyIdTypeTrack},
		{"spotify:playlist:abc123", SpotifyIdTypeTrack},
		{"something-else", SpotifyIdTypeTrack},
		{"", SpotifyIdTypeTrack},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got := InferSpotifyIdTypeFromContextUri(tt.uri)
			if got != tt.want {
				t.Errorf("InferSpotifyIdTypeFromContextUri(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestGidToBase62AllZeros(t *testing.T) {
	gid := make([]byte, 16)
	base62 := GidToBase62(gid)
	if len(base62) != 22 {
		t.Fatalf("length = %d, want 22", len(base62))
	}
	// All zeros should produce all '0' characters in base62.
	for _, c := range base62 {
		if c != '0' {
			t.Errorf("expected all '0' chars for zero GID, got %q", base62)
			break
		}
	}
}

func TestGidToBase62AllOnes(t *testing.T) {
	gid := bytes.Repeat([]byte{0xff}, 16)
	base62 := GidToBase62(gid)
	if len(base62) != 22 {
		t.Fatalf("length = %d, want 22", len(base62))
	}

	// Round-trip should work.
	back, err := Base62ToGid(base62)
	if err != nil {
		t.Fatalf("Base62ToGid error: %v", err)
	}
	if !bytes.Equal(gid, back) {
		t.Errorf("round-trip failed for all-ones GID: got %x", back)
	}
}
