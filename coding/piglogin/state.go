package piglogin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/configroot"
)

// DefaultID is the sprite used until one is selected.
const DefaultID = "pig-default"

type state struct {
	Variant string `json:"variant"`
}

// StatePath is where the selection lives under configHome. The directory name is PiG Standard's, so a selection made there
// carries over and the games find it.
func StatePath(configHome string) string {
	return filepath.Join(configHome, "state", "pig-standard", "login.json")
}

// LoadVariant reads the selection saved under configHome. A missing, unreadable or unknown selection is the default.
func LoadVariant(configHome string) Variant {
	return FindVariant(loadID(configHome))
}

// loadID reads the saved sprite ID under configHome, or "" when none can be read.
func loadID(configHome string) string {
	data, err := os.ReadFile(StatePath(configHome))
	if err != nil {
		return ""
	}
	var saved state
	if json.Unmarshal(data, &saved) != nil {
		return ""
	}
	return saved.Variant
}

// SaveVariant saves the selection under configHome in an owner-only file.
func SaveVariant(configHome, id string) error {
	path := StatePath(configHome)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create login state directory: %w", err)
	}
	data, err := json.Marshal(state{Variant: id})
	if err != nil {
		return fmt.Errorf("encode login state: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write login state: %w", err)
	}
	return nil
}

// active is the selection the header draws. The header renders on every frame, so it reads the state file once per
// config root and after Refresh, not per frame. It keeps the saved ID rather than the sprite: an extension's sprite drawn as
// the default until its extension registers it is drawn as itself from then on.
var active struct {
	mu     sync.Mutex
	home   string
	loaded bool
	id     string
}

// Active is the sprite the header shows now: the saved one, or the default when it is unknown or its extension has not
// registered it.
func Active() Variant {
	home, err := configroot.Resolve()
	if err != nil {
		return FindVariant(DefaultID)
	}
	active.mu.Lock()
	if !active.loaded || active.home != home {
		active.id, active.home, active.loaded = loadID(home), home, true
	}
	id := active.id
	active.mu.Unlock()
	return FindVariant(id)
}

// Activate saves the selection under the config root (internal/configroot, the root the Pigpen games read through the SDK's ConfigHome) and makes it the active sprite. An unknown ID or a failed save leaves the
// active sprite unchanged.
func Activate(id string) error {
	variant, ok := ByID(id)
	if !ok {
		return fmt.Errorf("unknown sprite %q; available: %s", id, IDs())
	}
	home, err := configroot.Resolve()
	if err != nil {
		return err
	}
	if err := SaveVariant(home, variant.ID); err != nil {
		return err
	}
	active.mu.Lock()
	active.id, active.home, active.loaded = variant.ID, home, true
	active.mu.Unlock()
	return nil
}

// Refresh makes the next Active read the saved selection again: a game or another PiG may have written it.
func Refresh() {
	active.mu.Lock()
	active.loaded = false
	active.mu.Unlock()
}
