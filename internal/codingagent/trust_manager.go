// trust_manager.go: project-trust resource detection, decision options, and store.
//
// Faithful port of upstream core/trust-manager.ts. Trust keys use canonical
// project paths, inherit from the nearest ancestor, and are serialized through
// a per-store sidecar lock.

package codingagent

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/nodefs"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/text"
)

var trustRequiringProjectConfigResources = [...]string{
	"settings.json",
	"mcp.json",
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
	abs, err := ResolvePath(cwd, "")
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
	// trust-manager.ts:187 canonicalizePath(resolvePath(process.env.HOME || homedir())): without HOME, Node's os.homedir() reads
	// the user database. An empty home would resolve to the working directory and hide its .agents/skills.
	userAgentsSkills := ""
	if home := cmp.Or(os.Getenv("HOME"), osHomedir()); home != "" {
		userAgentsSkills = filepath.Join(normalizeTrustCwd(home), ".agents", "skills")
	}
	current := normalizeTrustCwd(cwd)

	configDir := ProjectConfigDir(current)
	var probe nodefs.PathProbe
	for _, entry := range trustRequiringProjectConfigResources {
		if probe.Set(configDir, entry).Exists() {
			return true
		}
	}

	for {
		if !probe.Set(current, ".agents", "skills").Is(userAgentsSkills) && probe.Exists() {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

// osHomedir is Node's os.homedir(): libuv's uv_os_homedir reads HOME (USERPROFILE on Windows), and the user database when the
// variable is unset or empty.
func osHomedir() string {
	key := "HOME"
	if runtime.GOOS == "windows" {
		key = "USERPROFILE"
	}
	//perf:ok reached only when HOME is empty (cmp.Or fallback), once per trust check
	if home := os.Getenv(key); home != "" {
		return home
	}
	if current, err := user.Current(); err == nil {
		return current.HomeDir
	}
	return ""
}

// ProjectTrustStore reads and writes <agentDir>/trust.json.
type ProjectTrustStore struct {
	trustPath string
	mu        sync.Mutex
}

// NewProjectTrustStore opens the store rooted at agentDir.
func NewProjectTrustStore(agentDir string) *ProjectTrustStore {
	abs, err := ResolvePath(agentDir, "")
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

	content := text.StripBomBytes(data)
	if err := jsonparse.Validate(content); err != nil {
		return nil, fmt.Errorf("Failed to read trust store %s: %w", s.trustPath, err)
	}
	if !orderedjson.IsObject(content) {
		return nil, fmt.Errorf("Invalid trust store %s: expected an object", s.trustPath)
	}
	object, err := orderedjson.Parse(content)
	if err != nil {
		return nil, fmt.Errorf("Failed to read trust store %s: %w", s.trustPath, err)
	}
	out := make(map[string]*bool, object.Len())
	// Object.entries order decides which invalid value the error names: array-index keys first, then insertion order.
	for _, key := range object.Keys() {
		raw, _ := object.Get(key)
		switch string(raw) {
		case "true":
			out[key] = new(true)
		case "false":
			out[key] = new(false)
		case "null":
			out[key] = nil
		default:
			return nil, fmt.Errorf("Invalid trust store %s: value for %q must be true, false, or null", s.trustPath, key)
		}
	}
	return out, nil
}

func (s *ProjectTrustStore) write(data map[string]*bool) error {
	// writeTrustFile (trust-manager.ts:126-135) sorts the keys with Array.prototype.sort, which compares UTF-16 code units, and writes JSON.stringify(sorted, null, 2): no HTML escaping, array-index keys first.
	sorted := orderedjson.New()
	for _, key := range slices.SortedFunc(maps.Keys(data), func(a, b string) int {
		return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b))
	}) {
		value := json.RawMessage("null")
		if data[key] != nil {
			value = json.RawMessage(strconv.FormatBool(*data[key]))
		}
		sorted.Set(key, value)
	}
	encoded, err := stringifyIndented(rawObject(sorted))
	if err != nil {
		return fmt.Errorf("write trust store %s: %w", s.trustPath, err)
	}
	// writeTrustFile rethrows Node's mkdirSync and writeFileSync errors (trust-manager.ts:122-132).
	if err := nodeerrno.MkdirAll(filepath.Dir(s.trustPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.trustPath, append(encoded, '\n'), 0o644); err != nil {
		return nodeerrno.FromPathError(err)
	}
	return nil
}

func (s *ProjectTrustStore) withLock(fn func() error) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// acquireTrustLockSync rethrows Node's mkdirSync error (trust-manager.ts:137-140).
	if err := nodeerrno.MkdirAll(filepath.Dir(s.trustPath), 0o755); err != nil {
		return err
	}
	release, err := acquireSyncLockWithRetry(s.trustPath)
	if err != nil {
		return err
	}
	defer releaseSyncLock(release, &err)
	return fn()
}

// Get returns the nearest decision for cwd, or nil when none applies.
func (s *ProjectTrustStore) Get(cwd string) (ProjectTrustDecision, error) {
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
func (s *ProjectTrustStore) Set(cwd string, decision ProjectTrustDecision) error {
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

// ProjectTrustDecision is a saved project trust decision: true trusts, false distrusts, nil is undecided (trust-manager.ts:9, boolean | null).
type ProjectTrustDecision = *bool
