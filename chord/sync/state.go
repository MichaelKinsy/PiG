package sync

import (
	"context"
	"encoding/json"
	"fmt"
)

// Ports packages/chord/src/sync/state.ts

// FileSyncStateStore is sync state in a directory, one JSON file per session, replaced through a temporary file and a
// rename. Losing the directory loses nothing that matters: a copy without commits of its own takes the remote's head, and
// one with commits finds out by uploading.
type FileSyncStateStore struct {
	fs        SyncFileSystem
	directory string
}

var _ SyncStateStore = (*FileSyncStateStore)(nil)

// NewFileSyncStateStore is the state in directory of fs.
func NewFileSyncStateStore(fs SyncFileSystem, directory string) *FileSyncStateStore {
	return &FileSyncStateStore{fs: fs, directory: directory}
}

// stateFile is a state as its file holds it.
type stateFile struct {
	SyncedHead *Seq `json:"syncedHead,omitempty"`
	Pulling    *Seq `json:"pulling,omitempty"`
}

func (store *FileSyncStateStore) Get(ctx context.Context, sessionID string) (*SyncState, error) {
	path, err := store.path(ctx, sessionID, ".json")
	if err != nil {
		return nil, err
	}
	text, err := store.fs.ReadTextFile(ctx, path)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stored stateFile
	if err := json.Unmarshal([]byte(text), &stored); err != nil {
		return nil, fmt.Errorf("sync: the state of session %s: %w", sessionID, err)
	}
	return &SyncState{SyncedHead: stored.SyncedHead, Pulling: stored.Pulling}, nil
}

func (store *FileSyncStateStore) Set(ctx context.Context, sessionID string, state SyncState) error {
	if err := store.fs.CreateDir(ctx, store.directory, true); err != nil {
		return err
	}
	path, err := store.path(ctx, sessionID, ".json")
	if err != nil {
		return err
	}
	temporary, err := store.path(ctx, sessionID, ".json.tmp")
	if err != nil {
		return err
	}
	text, err := json.Marshal(stateFile(state))
	if err != nil {
		return err
	}
	if err := store.fs.WriteFile(ctx, temporary, text); err != nil {
		return err
	}
	return store.fs.RenameFile(ctx, temporary, path)
}

// Delete forgets a session, as when its copy is renamed or deleted.
func (store *FileSyncStateStore) Delete(ctx context.Context, sessionID string) error {
	path, err := store.path(ctx, sessionID, ".json")
	if err != nil {
		return err
	}
	return store.fs.Remove(ctx, path, false, true)
}

func (store *FileSyncStateStore) path(ctx context.Context, sessionID, suffix string) (string, error) {
	checked, err := CheckSessionID(sessionID)
	if err != nil {
		return "", err
	}
	return store.fs.JoinPath(ctx, []string{store.directory, checked + suffix})
}
