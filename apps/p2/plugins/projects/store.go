package projects

import (
	"context"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
)

// Ports apps/p2/plugins/projects/store.ts: `projects.json` observed: every valid save applies at once, an invalid one
// keeps the last valid projects.

// ProjectSnapshot is the projects as last read validly and why the last read failed (empty: it did not).
type ProjectSnapshot struct {
	Projects []contract.Project
	Error    string
}

// ProjectStore watches projects.json. Watching starts before loading. Invalid saves keep the last valid projects and say
// why. One observer per process.
type ProjectStore struct {
	observer *p2runtime.Observer

	mu        sync.Mutex
	snapshot  ProjectSnapshot
	listeners map[int]func(ProjectSnapshot)
	next      int
}

// NewProjectStore is the store of directory's projects.json, not yet started.
func NewProjectStore(directory string) *ProjectStore {
	store := &ProjectStore{snapshot: ProjectSnapshot{Projects: []contract.Project{}}, listeners: map[int]func(ProjectSnapshot){}}
	files := []p2runtime.ObservedFiles{{
		Env: nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: directory}),
		Targets: func(context.Context) ([]durableenv.WatchTarget, error) {
			return []durableenv.WatchTarget{{Path: ProjectsPath(directory)}}, nil
		},
	}}
	store.observer = p2runtime.NewObserver(files, func(ctx context.Context, retrying bool) bool {
		projects, err := LoadProjects(ctx, directory)
		store.mu.Lock()
		if err != nil {
			// An editor's save caught half-way: read once more before reporting it.
			if !retrying {
				store.mu.Unlock()
				return true
			}
			store.snapshot = ProjectSnapshot{
				Projects: store.snapshot.Projects,
				Error:    "Could not load projects.json (" + err.Error() + "); keeping the last valid projects",
			}
		} else {
			store.snapshot = ProjectSnapshot{Projects: projects}
		}
		snapshot := store.snapshot
		listeners := make([]func(ProjectSnapshot), 0, len(store.listeners))
		for _, listener := range store.listeners {
			listeners = append(listeners, listener)
		}
		store.mu.Unlock()
		for _, listener := range listeners {
			listener(snapshot)
		}
		return false
	}, 2*time.Second)
	return store
}

// Start watches, then loads; it returns after the first load.
func (store *ProjectStore) Start(ctx context.Context) error { return store.observer.Start(ctx) }

// Close stops watching.
func (store *ProjectStore) Close() { store.observer.Close() }

// Snapshot is the projects as last read validly.
func (store *ProjectStore) Snapshot() ProjectSnapshot {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.snapshot
}

// Subscribe calls listener with every load's snapshot from now on; the result stops it.
func (store *ProjectStore) Subscribe(listener func(ProjectSnapshot)) func() {
	store.mu.Lock()
	defer store.mu.Unlock()
	id := store.next
	store.next++
	store.listeners[id] = listener
	return func() {
		store.mu.Lock()
		defer store.mu.Unlock()
		delete(store.listeners, id)
	}
}
