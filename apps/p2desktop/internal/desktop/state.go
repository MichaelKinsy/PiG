package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// state is what the desktop app remembers between launches, in its own directory (never in p2's data directory, whose files
// belong to the server).
type state struct {
	// Port is the loopback port the server listened on last time. The web UI keeps its settings and drafts per origin
	// (localStorage), so the app asks for the same port again and takes a free one only when it is taken.
	Port int `json:"port,omitempty"`
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

func loadState(dir string) state {
	var s state
	b, err := os.ReadFile(statePath(dir))
	if err != nil || json.Unmarshal(b, &s) != nil || s.Port < 1024 || s.Port > 65535 {
		return state{}
	}
	return s
}

func saveState(dir string, s state) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), statePath(dir))
}
