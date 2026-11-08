package envs

import (
	"context"
	"sync"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/envs/store.ts: envs.json as the store last read it validly. Invalid saves keep the last valid
// configuration. One observer per process; watching starts before loading.

// EnvSnapshot is the configuration and why the last read failed (empty: it did not).
type EnvSnapshot struct {
	Configs EnvsConfig
	Error   string
}

// EnvStore watches envs.json.
type EnvStore struct {
	observer *p2runtime.Observer

	mu        sync.Mutex
	snapshot  EnvSnapshot
	listeners map[int]func(EnvSnapshot)
	next      int
}

// NewEnvStore is the store of directory's envs.json, not yet started.
func NewEnvStore(directory string) *EnvStore {
	store := &EnvStore{snapshot: EnvSnapshot{Configs: chordjson.NewObject(0)}, listeners: map[int]func(EnvSnapshot){}}
	files := []p2runtime.ObservedFiles{{
		Env: nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: directory}),
		Targets: func(context.Context) ([]durableenv.WatchTarget, error) {
			return []durableenv.WatchTarget{{Path: EnvsPath(directory)}}, nil
		},
	}}
	store.observer = p2runtime.NewObserver(files, func(ctx context.Context, retrying bool) bool {
		configs, err := LoadEnvs(ctx, directory)
		store.mu.Lock()
		if err != nil {
			if !retrying {
				store.mu.Unlock()
				return true
			}
			store.snapshot = EnvSnapshot{Configs: store.snapshot.Configs, Error: "Could not load envs.json; keeping last valid state"}
		} else {
			store.snapshot = EnvSnapshot{Configs: configs}
		}
		snapshot := store.snapshot
		listeners := make([]func(EnvSnapshot), 0, len(store.listeners))
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
func (store *EnvStore) Start(ctx context.Context) error { return store.observer.Start(ctx) }

// Reload loads again now and returns once loaded.
func (store *EnvStore) Reload(ctx context.Context) error { return store.observer.Reload(ctx) }

// Close stops watching.
func (store *EnvStore) Close() { store.observer.Close() }

// Snapshot is the configuration as last read validly.
func (store *EnvStore) Snapshot() EnvSnapshot {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.snapshot
}

// Subscribe calls listener with every load's snapshot from now on; the result stops it.
func (store *EnvStore) Subscribe(listener func(EnvSnapshot)) func() {
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
