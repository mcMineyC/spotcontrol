package spotcontrol

import (
	"encoding/hex"
	"testing"
)

func TestGenerateDeviceId_Length(t *testing.T) {
	id := GenerateDeviceId()
	if len(id) != 40 {
		t.Errorf("expected device ID length 40, got %d: %q", len(id), id)
	}
}

func TestGenerateDeviceId_ValidHex(t *testing.T) {
	id := GenerateDeviceId()
	decoded, err := hex.DecodeString(id)
	if err != nil {
		t.Errorf("device ID is not valid hex: %q: %v", id, err)
	}
	if len(decoded) != 20 {
		t.Errorf("expected 20 decoded bytes, got %d", len(decoded))
	}
}

func TestGenerateDeviceId_Uniqueness(t *testing.T) {
	const n = 100
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := GenerateDeviceId()
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate device ID generated on iteration %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}
