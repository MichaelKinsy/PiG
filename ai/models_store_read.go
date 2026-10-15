package ai

// Ports packages/coding-agent/src/core/models-store.ts.

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/profiling"
)

type modelsFileReload struct {
	cancel   context.CancelFunc
	done     chan struct{}
	readers  int
	data     []storedModels
	revision fileRevision
	err      error
}

type modelsFileReadState struct {
	mu       sync.Mutex
	data     []storedModels
	revision fileRevision
	reload   *modelsFileReload
}

// Pi shares only the first store path across instances; other paths keep instance-owned state.
var sharedModelsFileReadState struct {
	sync.Mutex
	path  string
	state *modelsFileReadState
}

func modelsReadStateForPath(path string) *modelsFileReadState {
	sharedModelsFileReadState.Lock()
	defer sharedModelsFileReadState.Unlock()
	if sharedModelsFileReadState.state == nil {
		sharedModelsFileReadState.path = path
		sharedModelsFileReadState.state = &modelsFileReadState{}
	}
	if sharedModelsFileReadState.path == path {
		return sharedModelsFileReadState.state
	}
	return &modelsFileReadState{}
}

// readLatest is AuthStorage.readLatest for models-store.json, operation snapshots included.
func (s *FileModelsStore) readLatest(ctx context.Context) ([]storedModels, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := s.readState
	if snapshot := fileReadSnapshotOf(ctx); snapshot != nil {
		published := func() (fileRevision, []storedModels) { return state.revision, state.data }
		return readHeld(ctx, &snapshot.models, state, &state.mu, published, s.readLatestFile, snapshot.modelsHoldable, context.Context.Err)
	}
	data, _, err := s.readLatestFile(ctx)
	return data, err
}

// readLatestFile is readLatest's stat and shared reload; it also returns the revision the entries were read at.
func (s *FileModelsStore) readLatestFile(ctx context.Context) ([]storedModels, fileRevision, error) {
	state := s.readState
	// As AuthStorage.readLatest: the stat runs before the lock, so readers do not queue behind each other's syscalls.
	profiling.ModelsFileStats.Add(1)
	revision, exists := fileRevisionOf(s.path)
	state.mu.Lock()
	if exists && state.revision == revision {
		data := state.data
		state.mu.Unlock()
		return data, revision, nil
	}
	reload := state.reload
	if reload == nil {
		operationCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		reload = &modelsFileReload{cancel: cancel, done: make(chan struct{})}
		state.reload = reload
		go s.reloadFromStorage(operationCtx, state, reload)
	}
	reload.readers++
	state.mu.Unlock()
	defer func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		reload.readers--
		if reload.readers == 0 && state.reload == reload {
			state.reload = nil
			reload.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return nil, fileRevision{}, ctx.Err()
	case <-reload.done:
		return reload.data, reload.revision, reload.err
	}
}

func (s *FileModelsStore) reloadFromStorage(ctx context.Context, state *modelsFileReadState, reload *modelsFileReload) {
	defer reload.cancel()
	reload.err = s.withLock(ctx, func(entries []storedModels) ([]storedModels, error) {
		profiling.ModelsFileStats.Add(1)
		revision, _ := fileRevisionOf(s.path)
		state.mu.Lock()
		state.data = entries
		state.revision = revision
		if state.reload == reload {
			state.reload = nil
		}
		state.mu.Unlock()
		reload.data, reload.revision = entries, revision
		return nil, nil
	})
	state.mu.Lock()
	if state.reload == reload {
		state.reload = nil
	}
	close(reload.done)
	state.mu.Unlock()
}
