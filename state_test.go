package spotcontrol

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	expiry := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	original := &AppState{
		DeviceId:          "abcdef0123456789abcdef0123456789abcdef01",
		Username:          "testuser",
		StoredCredentials: []byte{0xDE, 0xAD, 0xBE, 0xEF},
		OAuthAccessToken:  "access-token-abc",
		OAuthRefreshToken: "refresh-token-xyz",
		OAuthTokenType:    "Bearer",
		OAuthExpiry:       expiry,
	}

	if err := SaveState(path, original); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	// Verify file permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	// On Unix, check that the file is not world-readable.
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		t.Errorf("expected file permissions 0600, got %04o", perm)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadState returned nil")
	}

	if loaded.DeviceId != original.DeviceId {
		t.Errorf("DeviceId mismatch: got %q, want %q", loaded.DeviceId, original.DeviceId)
	}
	if loaded.Username != original.Username {
		t.Errorf("Username mismatch: got %q, want %q", loaded.Username, original.Username)
	}
	if string(loaded.StoredCredentials) != string(original.StoredCredentials) {
		t.Errorf("StoredCredentials mismatch: got %x, want %x", loaded.StoredCredentials, original.StoredCredentials)
	}
	if loaded.OAuthAccessToken != original.OAuthAccessToken {
		t.Errorf("OAuthAccessToken mismatch: got %q, want %q", loaded.OAuthAccessToken, original.OAuthAccessToken)
	}
	if loaded.OAuthRefreshToken != original.OAuthRefreshToken {
		t.Errorf("OAuthRefreshToken mismatch: got %q, want %q", loaded.OAuthRefreshToken, original.OAuthRefreshToken)
	}
	if loaded.OAuthTokenType != original.OAuthTokenType {
		t.Errorf("OAuthTokenType mismatch: got %q, want %q", loaded.OAuthTokenType, original.OAuthTokenType)
	}
	if !loaded.OAuthExpiry.Equal(original.OAuthExpiry) {
		t.Errorf("OAuthExpiry mismatch: got %v, want %v", loaded.OAuthExpiry, original.OAuthExpiry)
	}
}

func TestLoadStateFileNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState should not return error for missing file, got: %v", err)
	}
	if state != nil {
		t.Fatalf("LoadState should return nil for missing file, got: %+v", state)
	}
}

func TestLoadStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	if err := os.WriteFile(path, []byte("{invalid json!!!"), 0600); err != nil {
		t.Fatalf("failed writing test file: %v", err)
	}

	_, err := LoadState(path)
	if err == nil {
		t.Fatal("LoadState should return error for invalid JSON")
	}
}

func TestSaveStateMinimalState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "minimal.json")

	state := &AppState{
		DeviceId: "0000000000000000000000000000000000000000",
	}

	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadState returned nil")
	}
	if loaded.DeviceId != state.DeviceId {
		t.Errorf("DeviceId mismatch: got %q, want %q", loaded.DeviceId, state.DeviceId)
	}
	if loaded.Username != "" {
		t.Errorf("expected empty Username, got %q", loaded.Username)
	}
	if loaded.StoredCredentials != nil {
		t.Errorf("expected nil StoredCredentials, got %x", loaded.StoredCredentials)
	}
	if loaded.OAuthAccessToken != "" {
		t.Errorf("expected empty OAuthAccessToken, got %q", loaded.OAuthAccessToken)
	}
}

func TestSaveStateOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	first := &AppState{
		DeviceId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Username: "first_user",
	}
	if err := SaveState(path, first); err != nil {
		t.Fatalf("SaveState (first) failed: %v", err)
	}

	second := &AppState{
		DeviceId: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Username: "second_user",
	}
	if err := SaveState(path, second); err != nil {
		t.Fatalf("SaveState (second) failed: %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if loaded.DeviceId != second.DeviceId {
		t.Errorf("expected overwritten DeviceId %q, got %q", second.DeviceId, loaded.DeviceId)
	}
	if loaded.Username != second.Username {
		t.Errorf("expected overwritten Username %q, got %q", second.Username, loaded.Username)
	}
}
