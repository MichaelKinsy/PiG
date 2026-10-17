package ai

// Ports packages/coding-agent/src/core/auth-storage.ts

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

type authFileReload struct {
	cancel   context.CancelFunc
	done     chan struct{}
	readers  int
	creds    *authStorageData
	revision fileRevision
	err      error
}

type authReadState struct {
	mu       sync.Mutex
	revision fileRevision
	creds    *authStorageData
	reload   *authFileReload
}

// Pi retains only the first auth path's read state for the process. Stores for other paths own independent states; they do not replace or expand this cache.
var sharedAuthFileReadState struct {
	sync.Mutex
	path  string
	state *authReadState
}

func authReadStateForPath(path string) *authReadState {
	sharedAuthFileReadState.Lock()
	defer sharedAuthFileReadState.Unlock()
	if sharedAuthFileReadState.state != nil && sharedAuthFileReadState.path == path {
		return sharedAuthFileReadState.state
	}
	state := &authReadState{creds: newAuthStorageData(nil)}
	if sharedAuthFileReadState.state == nil {
		sharedAuthFileReadState.path = path
		sharedAuthFileReadState.state = state
	}
	return state
}

// fileRevisionOf is authFileRevision. Tests wrap it to observe where reads stat.
var fileRevisionOf = authFileRevision

func (a *AuthStorage) reloadInitialSnapshot() {
	state := a.read
	state.mu.Lock()
	defer state.mu.Unlock()
	if revision, ok := authFileRevision(a.path); ok && revision == state.revision {
		return
	}
	// Pi's constructor calls reload, which preserves the previous snapshot on lock/read/parse failure.
	var creds *authStorageData
	var revision fileRevision
	if err := a.withFileLock(context.Background(), false, func(_ *pilock.Lock) error {
		var err error
		creds, err = a.loadLocked()
		revision, _ = authFileRevision(a.path)
		return err
	}); err == nil {
		state.creds, state.revision = creds, revision
	}
}

// readCredentialData mirrors readLatestData's unsignalled failure fallback. Legacy synchronous Load/Get callers retain their error-returning contract.
func (a *AuthStorage) readCredentialData(ctx context.Context) (*authStorageData, error) {
	creds, err := a.readLatest(ctx)
	if err != nil && ctx.Done() == nil {
		a.read.mu.Lock()
		creds = a.read.creds
		a.read.mu.Unlock()
		return creds, nil
	}
	return creds, err
}

// readLatest shares a reload without lending any one reader's cancellation to the other readers. The state owns the worker until it releases its file lock and publishes completion. Its last departing reader cancels unneeded work.
// Within an operation that carries a file-read snapshot (WithFileReadSnapshot), a read answers from the published credentials without a stat while they are still the version the operation last statted.
func (a *AuthStorage) readLatest(ctx context.Context) (*authStorageData, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	state := a.read
	if snapshot := fileReadSnapshotOf(ctx); snapshot != nil {
		published := func() (fileRevision, *authStorageData) { return state.revision, state.creds }
		return readHeld(ctx, &snapshot.auth, state, &state.mu, published, a.readLatestFile, (*authStorageData).runsNoCommands, context.Cause)
	}
	creds, _, err := a.readLatestFile(ctx)
	return creds, err
}

// readLatestFile is readLatest's stat and shared reload. It also returns the revision the credentials were read at: the stat's, or the reload's.
func (a *AuthStorage) readLatestFile(ctx context.Context) (*authStorageData, fileRevision, error) {
	state := a.read
	// The stat runs before the lock: the read takes effect at its stat, inside its own call window, and concurrent readers do not queue behind each other's syscalls. A stale stat can only start an extra reload.
	revision, ok := fileRevisionOf(a.path)
	state.mu.Lock()
	if ok && revision == state.revision {
		creds := state.creds
		state.mu.Unlock()
		return creds, revision, nil
	}
	reload := state.reload
	if reload == nil {
		workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		reload = &authFileReload{cancel: cancel, done: make(chan struct{})}
		state.reload = reload
		go a.reloadFromStorage(workCtx, reload)
	}
	reload.readers++
	state.mu.Unlock()
	defer func() {
		state.mu.Lock()
		reload.readers--
		if reload.readers == 0 && state.reload == reload {
			state.reload = nil
			reload.cancel()
		}
		state.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return nil, fileRevision{}, context.Cause(ctx)
	case <-reload.done:
		return reload.creds, reload.revision, reload.err
	}
}

func (a *AuthStorage) reloadFromStorage(ctx context.Context, reload *authFileReload) {
	state := a.read
	reload.err = a.withFileLock(ctx, true, func(lock *pilock.Lock) error {
		creds, err := a.loadLocked()
		if err != nil {
			return err
		}
		if err := lock.Check(); err != nil {
			return err
		}
		revision, _ := fileRevisionOf(a.path)
		state.mu.Lock()
		if state.reload == reload {
			state.creds = creds
			state.revision = revision
		}
		state.mu.Unlock()
		reload.creds, reload.revision = creds, revision
		return nil
	})
	state.mu.Lock()
	if state.reload == reload {
		state.reload = nil
	}
	close(reload.done)
	state.mu.Unlock()
	reload.cancel()
}
