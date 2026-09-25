package angrypigs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type savedState struct {
	HighScore int `json:"highScore"`
}

func loadHighScore(configHome string) int {
	file, err := os.Open(statePath(configHome))
	if err != nil {
		return 0
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 4096 {
		return 0
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4096))
	decoder.DisallowUnknownFields()
	var state savedState
	if decoder.Decode(&state) != nil || state.HighScore < 0 {
		return 0
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return 0
	}
	return state.HighScore
}

func saveHighScore(configHome string, highScore int) error {
	if highScore < 0 {
		return fmt.Errorf("high score must not be negative")
	}
	dir := filepath.Dir(statePath(configHome))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Angry Pigs state directory: %w", err)
	}
	data, err := json.Marshal(savedState{HighScore: highScore})
	if err != nil {
		return fmt.Errorf("encode Angry Pigs state: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".angrypigs-*")
	if err != nil {
		return fmt.Errorf("create Angry Pigs state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect Angry Pigs state: %w", err)
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write Angry Pigs state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync Angry Pigs state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Angry Pigs state: %w", err)
	}
	if err := os.Rename(temporaryPath, statePath(configHome)); err != nil {
		if removeErr := os.Remove(statePath(configHome)); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove previous Angry Pigs state: %w", removeErr)
		}
		if retryErr := os.Rename(temporaryPath, statePath(configHome)); retryErr != nil {
			return fmt.Errorf("replace Angry Pigs state: %w", retryErr)
		}
	}
	return nil
}

func statePath(configHome string) string {
	return filepath.Join(configHome, "state", "pig-standard", "angrypigs.json")
}
