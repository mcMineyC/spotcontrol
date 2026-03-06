package spotcontrol

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// LoadState reads an AppState from a JSON file at the given path.
// If the file does not exist, it returns (nil, nil) — not an error — so
// callers can treat a missing file as "no prior state" without extra checks.
func LoadState(path string) (*AppState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var state AppState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// SaveState writes an AppState as pretty-printed JSON to the given path with
// file mode 0600 (owner read/write only) to protect credentials.
func SaveState(path string, state *AppState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
