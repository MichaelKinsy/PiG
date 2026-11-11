package views

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/views/library.ts: the owner's library of saved views (server): one JSON file per view in
// `<data>/views/`, read once at start and written through this store, which tells listeners of each change.

// SavedView is a view in the owner's library.
type SavedView struct {
	Name        string  `json:"name"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Kind        string  `json:"kind"`
	Path        string  `json:"path,omitempty"`
	HTML        *string `json:"html,omitempty"`
	State       any     `json:"state"`
	Saved       float64 `json:"saved"`
}

// SavedInfo is a saved view without its HTML and state, for lists.
type SavedInfo struct {
	Name        string  `json:"name"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Kind        string  `json:"kind"`
	Saved       float64 `json:"saved"`
}

// maxFile is a saved view's file at most this large: its HTML and state with room for the rest.
const maxFile = contract.MaxViewHTML + contract.MaxViewState + 64*1024

var isSavedView = func() func(any) bool {
	validator, err := schema.Compile(contract.SavedViewSchema, schema.CompileOptions{})
	if err != nil {
		panic(err)
	}
	return func(value any) bool {
		ok, err := validator.Check(value)
		return err == nil && ok
	}
}()

// Library is the saved views of one data directory.
type Library struct {
	dir       string
	mu        sync.Mutex
	listeners map[int]func()
	next      int
	saved     []SavedInfo
}

// NewLibrary is the library of dataDir.
func NewLibrary(dataDir string) *Library {
	return &Library{dir: filepath.Join(dataDir, "views"), listeners: map[int]func(){}}
}

// Saved is every saved view, newest first.
func (library *Library) Saved() []SavedInfo {
	library.mu.Lock()
	defer library.mu.Unlock()
	return append([]SavedInfo{}, library.saved...)
}

// Load reads the directory; files that are not saved views are skipped.
func (library *Library) Load() error {
	entries, err := os.ReadDir(library.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	saved := []SavedInfo{}
	for _, entry := range entries {
		name, ok := cutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		if view, _ := library.Get(name); view != nil {
			saved = append(saved, infoOf(*view))
		}
	}
	sort.SliceStable(saved, func(i, j int) bool { return saved[i].Saved > saved[j].Saved })
	library.mu.Lock()
	library.saved = saved
	library.mu.Unlock()
	return nil
}

func cutSuffix(text, suffix string) (string, bool) {
	if len(text) > len(suffix) && text[len(text)-len(suffix):] == suffix {
		return text[:len(text)-len(suffix)], true
	}
	return "", false
}

// Subscribe calls listener after each change; the result stops it.
func (library *Library) Subscribe(listener func()) func() {
	library.mu.Lock()
	defer library.mu.Unlock()
	id := library.next
	library.next++
	library.listeners[id] = listener
	return func() {
		library.mu.Lock()
		defer library.mu.Unlock()
		delete(library.listeners, id)
	}
}

// Get is saved view name, or nil (a missing or unreadable file).
func (library *Library) Get(name string) (*SavedView, error) {
	if !contract.ViewID.MatchString(name) {
		return nil, nil
	}
	// The files are the owner's to edit: read them as untrusted.
	text, err := os.ReadFile(filepath.Join(library.dir, name+".json"))
	if err != nil || len(text) > maxFile {
		return nil, nil
	}
	value, err := chordjson.Decode(text)
	if err != nil || !isSavedView(value) {
		return nil, nil
	}
	var view SavedView
	if json.Unmarshal(text, &view) != nil || view.Name != name {
		return nil, nil
	}
	return &view, nil
}

// Save saves view under its name, replacing a view saved under it before.
func (library *Library) Save(view SavedView) (SavedInfo, error) {
	text, err := json.MarshalIndent(view, "", "\t")
	if err != nil {
		return SavedInfo{}, err
	}
	if len(text) > maxFile {
		return SavedInfo{}, errors.New("The view is too large to save")
	}
	if err := os.MkdirAll(library.dir, 0o755); err != nil {
		return SavedInfo{}, err
	}
	path := filepath.Join(library.dir, view.Name+".json")
	if err := os.WriteFile(path+".tmp", text, 0o644); err != nil {
		return SavedInfo{}, err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return SavedInfo{}, err
	}
	info := infoOf(view)
	library.mu.Lock()
	kept := []SavedInfo{info}
	for _, each := range library.saved {
		if each.Name != view.Name {
			kept = append(kept, each)
		}
	}
	library.saved = kept
	library.mu.Unlock()
	library.changed()
	return info, nil
}

// Remove removes saved view name (an unknown one: nothing).
func (library *Library) Remove(name string) error {
	if !contract.ViewID.MatchString(name) {
		return nil
	}
	if err := os.Remove(filepath.Join(library.dir, name+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	library.mu.Lock()
	kept := library.saved[:0:0]
	for _, each := range library.saved {
		if each.Name != name {
			kept = append(kept, each)
		}
	}
	library.saved = kept
	library.mu.Unlock()
	library.changed()
	return nil
}

func (library *Library) changed() {
	library.mu.Lock()
	listeners := make([]func(), 0, len(library.listeners))
	for _, listener := range library.listeners {
		listeners = append(listeners, listener)
	}
	library.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

func infoOf(view SavedView) SavedInfo {
	return SavedInfo{Name: view.Name, Title: view.Title, Kind: view.Kind, Saved: view.Saved, Description: view.Description}
}
