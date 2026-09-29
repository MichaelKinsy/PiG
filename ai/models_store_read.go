package ai

// Ports packages/coding-agent/src/core/models-store.ts.

import (
	"context"
	"sync"
)

type modelsFileReload struct {
	cancel  context.CancelFunc
	done    chan struct{}
	readers int
	data    []storedModels
	err     error
}

type modelsFileReadState struct {
	mu       sync.Mutex
	data     []storedModels
	revision string
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

func (s *FileModelsStore) readLatest(ctx context.Context) ([]storedModels, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := s.readState
	state.mu.Lock()
	revision, exists := authFileRevision(s.path)
	if exists && state.revision == revision {
		data := state.data
		state.mu.Unlock()
		return data, nil
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
		return nil, ctx.Err()
	case <-reload.done:
		return reload.data, reload.err
	}
}

func (s *FileModelsStore) reloadFromStorage(ctx context.Context, state *modelsFileReadState, reload *modelsFileReload) {
	defer reload.cancel()
	reload.err = s.withLock(ctx, func(entries []storedModels) ([]storedModels, error) {
		state.mu.Lock()
		state.data = entries
		state.revision, _ = authFileRevision(s.path)
		if state.reload == reload {
			state.reload = nil
		}
		state.mu.Unlock()
		reload.data = entries
		return nil, nil
	})
	state.mu.Lock()
	if state.reload == reload {
		state.reload = nil
	}
	close(reload.done)
	state.mu.Unlock()
}
