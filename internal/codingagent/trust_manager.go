// trust_manager.go: project-trust resource detection, decision options, and store.
//
// Faithful port of upstream core/trust-manager.ts. Trust keys use canonical
// project paths, inherit from the nearest ancestor, and are serialized through
// a per-store sidecar lock.

package codingagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/internal/text"
)

var trustRequiringProjectConfigResources = [...]string{
	"settings.json",
	"extensions",
	"skills",
	"prompts",
	"themes",
	"SYSTEM.md",
	"APPEND_SYSTEM.md",
}

// ProjectTrustStoreEntry is the nearest stored decision for a cwd.
type ProjectTrustStoreEntry struct {
	Path     string
	Decision bool
}

// ProjectTrustUpdate sets (true/false) or clears (nil) a path's decision.
type ProjectTrustUpdate struct {
	Path     string
	Decision *bool
}

// ProjectTrustOption is one row in a project-trust selector.
type ProjectTrustOption struct {
	Label     string
	Trusted   bool
	Updates   []ProjectTrustUpdate
	SavedPath string
}

func normalizeTrustCwd(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	return CanonicalizePath(abs)
}

// GetProjectTrustPath returns the canonical trust key for a cwd.
func GetProjectTrustPath(cwd string) string { return normalizeTrustCwd(cwd) }

// GetProjectTrustParentPath returns the parent trust key, or "" at the root.
func GetProjectTrustParentPath(cwd string) string {
	trustPath := GetProjectTrustPath(cwd)
	parent := filepath.Dir(trustPath)
	if parent == trustPath {
		return ""
	}
	return parent
}

// GetProjectTrustOptions builds the upstream-ordered selector choices.
func GetProjectTrustOptions(cwd string, includeSessionOnly bool) []ProjectTrustOption {
	trustPath := GetProjectTrustPath(cwd)
	options := []ProjectTrustOption{{
		Label:     "Trust",
		Trusted:   true,
		Updates:   []ProjectTrustUpdate{{Path: trustPath, Decision: new(true)}},
		SavedPath: trustPath,
	}}
	if parent := GetProjectTrustParentPath(cwd); parent != "" {
		options = append(options, ProjectTrustOption{
			Label:   "Trust parent folder (" + parent + ")",
			Trusted: true,
			Updates: []ProjectTrustUpdate{
				{Path: parent, Decision: new(true)},
				{Path: trustPath, Decision: nil},
			},
			SavedPath: parent,
		})
	}
	if includeSessionOnly {
		options = append(options, ProjectTrustOption{Label: "Trust (this session only)", Trusted: true})
	}
	options = append(options, ProjectTrustOption{
		Label:     "Do not trust",
		Trusted:   false,
		Updates:   []ProjectTrustUpdate{{Path: trustPath, Decision: new(false)}},
		SavedPath: trustPath,
	})
	if includeSessionOnly {
		options = append(options, ProjectTrustOption{Label: "Do not trust (this session only)", Trusted: false})
	}
	return options
}

// HasTrustRequiringProjectResources reports whether cwd has project-local
// inputs governed by project trust. Project config entries are checked only in
// cwd; .agents/skills is checked from cwd through every ancestor. The user's
// ~/.agents/skills is global input and never requires project trust.
func HasTrustRequiringProjectResources(cwd string) bool {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	home = normalizeTrustCwd(home)
	userAgentsSkills := filepath.Join(home, ".agents", "skills")
	current := normalizeTrustCwd(cwd)

	configDir := filepath.Join(current, CONFIG_DIR_NAME)
	for _, entry := range trustRequiringProjectConfigResources {
		if _, err := os.Stat(filepath.Join(configDir, entry)); err == nil {
			return true
		}
	}

	for {
		agentsSkills := filepath.Join(current, ".agents", "skills")
		if agentsSkills != userAgentsSkills {
			if _, err := os.Stat(agentsSkills); err == nil {
				return true
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

// ProjectTrustStore reads and writes <agentDir>/trust.json.
type ProjectTrustStore struct {
	trustPath string
	mu        sync.Mutex
}

// NewProjectTrustStore opens the store rooted at agentDir.
func NewProjectTrustStore(agentDir string) *ProjectTrustStore {
	abs, err := filepath.Abs(agentDir)
	if err != nil {
		abs = agentDir
	}
	return &ProjectTrustStore{trustPath: filepath.Join(abs, "trust.json")}
}

func (s *ProjectTrustStore) read() (map[string]*bool, error) {
	data, err := os.ReadFile(s.trustPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*bool{}, nil
		}
		return nil, fmt.Errorf("Failed to read trust store %s: %w", s.trustPath, err)
	}

	var parsed any
	if err := json.Unmarshal(text.StripBomBytes(data), &parsed); err != nil {
		return nil, fmt.Errorf("Failed to read trust store %s: %w", s.trustPath, err)
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid trust store %s: expected an object", s.trustPath)
	}
	out := make(map[string]*bool, len(object))
	for key, value := range object {
		switch typed := value.(type) {
		case bool:
			out[key] = new(typed)
		case nil:
			out[key] = nil
		default:
			return nil, fmt.Errorf("Invalid trust store %s: value for %q must be true, false, or null", s.trustPath, key)
		}
	}
	return out, nil
}

func (s *ProjectTrustStore) write(data map[string]*bool) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("write trust store %s: %w", s.trustPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(s.trustPath), 0o755); err != nil {
		return fmt.Errorf("create trust store directory: %w", err)
	}
	if err := os.WriteFile(s.trustPath, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write trust store %s: %w", s.trustPath, err)
	}
	return nil
}

func (s *ProjectTrustStore) withLock(fn func() error) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.trustPath), 0o755); err != nil {
		return fmt.Errorf("create trust store directory: %w", err)
	}
	lock := flock.New(s.trustPath + ".lock")
	locked, err := acquireSyncLockWithRetry(lock)
	if err != nil {
		return fmt.Errorf("acquire trust store lock: %w", err)
	}
	if !locked {
		return errors.New("failed to acquire trust store lock")
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("release trust store lock: %w", unlockErr))
		}
	}()
	return fn()
}

// Get returns the nearest decision for cwd, or nil when none applies.
func (s *ProjectTrustStore) Get(cwd string) (*bool, error) {
	entry, err := s.GetEntry(cwd)
	if err != nil || entry == nil {
		return nil, err
	}
	return new(entry.Decision), nil
}

// GetEntry returns the nearest stored decision, walking through parent paths.
func (s *ProjectTrustStore) GetEntry(cwd string) (entry *ProjectTrustStoreEntry, err error) {
	err = s.withLock(func() error {
		data, readErr := s.read()
		if readErr != nil {
			return readErr
		}
		for current := normalizeTrustCwd(cwd); ; current = filepath.Dir(current) {
			if value, ok := data[current]; ok && value != nil {
				entry = &ProjectTrustStoreEntry{Path: current, Decision: *value}
				return nil
			}
			parent := filepath.Dir(current)
			if parent == current {
				return nil
			}
		}
	})
	return entry, err
}

// Set sets or clears the canonical decision for cwd.
func (s *ProjectTrustStore) Set(cwd string, decision *bool) error {
	return s.SetMany([]ProjectTrustUpdate{{Path: cwd, Decision: decision}})
}

// SetMany applies one locked read-modify-write batch. A nil decision deletes
// that key; unrelated null entries already present in the store are preserved.
func (s *ProjectTrustStore) SetMany(updates []ProjectTrustUpdate) error {
	return s.withLock(func() error {
		data, err := s.read()
		if err != nil {
			return err
		}
		for _, update := range updates {
			key := normalizeTrustCwd(update.Path)
			if update.Decision == nil {
				delete(data, key)
				continue
			}
			data[key] = new(*update.Decision)
		}
		return s.write(data)
	})
}
