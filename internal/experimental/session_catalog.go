package experimental

// Ports packages/coding-agent/src/experimental/session-catalog.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SessionCatalogMetadata identifies one server-hosted Session: a directory holding meta.json and the worker-owned session.sqlite. It is also the StrictObject SessionWorkerMetadataSchema (session-worker.ts:69-74) that the manager sends in launch options and the worker echoes in worker_ready. CreatedAt is a JavaScript number, so any finite JSON number is valid. Cwd is the working directory the Session's agent runs in; Path is the Session directory, which workers lock and whose storage they own.
// upstream: packages/coding-agent/src/experimental/session-catalog.ts:SessionCatalogMetadata
type SessionCatalogMetadata struct {
	ID        string  `json:"id"`
	CreatedAt float64 `json:"createdAt"`
	Cwd       string  `json:"cwd"`
	Path      string  `json:"path"`
}

// SessionID is the Session's ID, as routing's SessionMetadata requires.
func (metadata SessionCatalogMetadata) SessionID() string { return metadata.ID }

const (
	sessionMetadataFile = "meta.json"
	sessionStorageFile  = "session.sqlite"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// IsSessionID reports whether id is a valid Session ID and so a safe directory name.
func IsSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

// SessionStoragePath is the durable storage file of a Session. Only the Session's worker opens it.
func SessionStoragePath(metadata SessionCatalogMetadata) string {
	return filepath.Join(metadata.Path, sessionStorageFile)
}

// ListSessions returns every Session in the directory. An absent directory has none, and an entry without valid metadata is skipped.
func ListSessions(sessionDir string) ([]SessionCatalogMetadata, error) {
	entries, err := os.ReadDir(sessionDir)
	if errors.Is(err, fs.ErrNotExist) {
		// Windows reports ReadDir of a regular file as a missing path; only a truly absent path is an empty catalog.
		if info, statErr := os.Stat(sessionDir); statErr == nil && !info.IsDir() {
			return nil, fmt.Errorf("session directory %s is not a directory", sessionDir)
		}
		return []SessionCatalogMetadata{}, nil
	}
	if err != nil {
		return nil, err
	}
	read := make([]*SessionCatalogMetadata, len(entries))
	var reads sync.WaitGroup
	for i, entry := range entries {
		if !IsSessionID(entry.Name()) {
			continue
		}
		reads.Go(func() { read[i] = ReadSession(sessionDir, entry.Name()) })
	}
	reads.Wait()
	sessions := []SessionCatalogMetadata{}
	for _, metadata := range read {
		if metadata != nil {
			sessions = append(sessions, *metadata)
		}
	}
	return sessions, nil
}

// ReadSession returns one Session by ID, or nil when its ID or its meta.json is invalid or absent. A metadata file that cannot be read or parsed is the same as an absent one, as in Pi.
func ReadSession(sessionDir, id string) *SessionCatalogMetadata {
	if !IsSessionID(id) {
		return nil
	}
	path := filepath.Join(sessionDir, id)
	raw, err := os.ReadFile(filepath.Join(path, sessionMetadataFile))
	if err != nil {
		return nil
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return nil
	}
	var createdAt float64
	var cwd string
	// JSON null is neither a number nor a string.
	if !isJSONValue(members["createdAt"]) || json.Unmarshal(members["createdAt"], &createdAt) != nil || !isJSONValue(members["cwd"]) || json.Unmarshal(members["cwd"], &cwd) != nil {
		return nil
	}
	return &SessionCatalogMetadata{ID: id, CreatedAt: createdAt, Cwd: cwd, Path: path}
}

// CreateSessionOptions selects the new Session's ID, which defaults to a random UUID, and its working directory.
type CreateSessionOptions struct {
	ID  *string
	Cwd string
}

// CreateSession creates an empty Session. Its worker creates the storage on first open. A Session that already exists is an error.
func CreateSession(sessionDir string, options CreateSessionOptions) (SessionCatalogMetadata, error) {
	id := uuid.NewString()
	if options.ID != nil {
		id = *options.ID
	}
	if !IsSessionID(id) {
		return SessionCatalogMetadata{}, fmt.Errorf("Invalid session ID: %s", id)
	}
	path := filepath.Join(sessionDir, id)
	if err := os.MkdirAll(sessionDir, 0o777); err != nil {
		return SessionCatalogMetadata{}, err
	}
	if err := os.Mkdir(path, 0o777); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return SessionCatalogMetadata{}, fmt.Errorf("Session %s already exists", id)
		}
		return SessionCatalogMetadata{}, err
	}
	metadata := SessionCatalogMetadata{ID: id, CreatedAt: float64(time.Now().UnixMilli()), Cwd: options.Cwd, Path: path}
	var file bytes.Buffer
	encoder := json.NewEncoder(&file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "\t")
	if err := encoder.Encode(struct {
		CreatedAt float64 `json:"createdAt"`
		Cwd       string  `json:"cwd"`
	}{metadata.CreatedAt, metadata.Cwd}); err != nil {
		return SessionCatalogMetadata{}, err
	}
	if err := os.WriteFile(filepath.Join(path, sessionMetadataFile), file.Bytes(), 0o666); err != nil {
		return SessionCatalogMetadata{}, err
	}
	return metadata, nil
}

// DeleteSession deletes a Session directory. Its worker must be closed first.
func DeleteSession(metadata SessionCatalogMetadata) error { return os.RemoveAll(metadata.Path) }

// isJSONValue reports whether raw is a present, non-null member.
func isJSONValue(raw json.RawMessage) bool { return len(raw) != 0 && string(raw) != "null" }
