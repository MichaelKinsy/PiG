// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// ─── SessionManager ───────────────────────────────────────────────────────────

// SessionManager owns the on-disk sessions directory for a given cwd
// and tracks the active session.
type SessionManager struct {
	cwd        string
	sessionDir string
	current    *Session
	mu         sync.RWMutex
}

// NewSessionManager creates a SessionManager for the given working dir.
func NewSessionManager(cwd string) *SessionManager {
	return &SessionManager{
		cwd:        cwd,
		sessionDir: defaultSessionDir(cwd),
	}
}

// NewSessionManagerWithDir overrides the default session directory
// (tests use this; production callers pass the result of
// defaultSessionDir).
func NewSessionManagerWithDir(cwd, dir string) *SessionManager {
	return &SessionManager{cwd: cwd, sessionDir: dir}
}

// defaultSessionDir returns <agent-dir>/sessions/<encoded-cwd>/.
// Mirrors upstream `getDefaultSessionDirPath`
// (.upstream/current/packages/coding-agent/src/core/session-manager.ts).
// This keeps the same relative path upstream Pi uses, modulo the config-root
// rename from ~/.pi to ~/.pig.
func defaultSessionDir(cwd string) string {
	return GetDefaultSessionDirPath(cwd, AgentDir())
}

// GetDefaultSessionDirPath derives storage from the selected agent directory, without consulting another configuration root.
func GetDefaultSessionDirPath(cwd, agentDir string) string {
	return filepath.Join(agentDir, "sessions", encodeCwdForSessionDir(cwd))
}

// encodeCwdForSessionDir produces upstream's `--<path>--` directory
// name from a cwd path. Steps mirror `session-manager.ts:429`:
//  1. Strip a leading `/` or `\` (one only).
//  2. Replace any `/`, `\`, or `:` with `-`.
//  3. Wrap in `--...--` bookends.
//
// Examples (Unix; pig is Unix-only):
//
//	/tmp/foo            -> --tmp-foo--
//	/Users/example/work -> --Users-example-work--
//	(empty)             -> ----
func encodeCwdForSessionDir(cwd string) string {
	s := cwd
	if len(s) > 0 && (s[0] == '/' || s[0] == '\\') {
		s = s[1:]
	}
	s = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(s)
	return "--" + s + "--"
}

// Create validates a supplied session ID or generates an omitted one, then assigns the deferred session file path. parentSessionPath records a source session when supplied.
func (sm *SessionManager) Create(id, parentSessionPath string) (*Session, error) {
	var option *string
	if id != "" {
		option = &id
	}
	sess, err := newSessionWithOptions(sm.cwd, option, parentSessionPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sm.sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionmanager: mkdir: %w", err)
	}
	// File name: <ts>_<id>.jsonl (matches upstream ordering convention
	// so directory listings sort chronologically).
	filename := fileTimestamp(sess.header.Timestamp) + "_" + sess.ID() + ".jsonl"
	path := filepath.Join(sm.sessionDir, filename)
	sess.path = path
	sess.sessionDir = sm.sessionDir

	// Do not write the file yet. Mirrors upstream SessionManager.newSession,
	// which sets the path but defers the first disk write to _persist on the
	// first user or assistant message. This keeps abandoned sessions (opened
	// but never used) off disk instead of leaving empty .jsonl files.

	sm.mu.Lock()
	sm.current = sess
	sm.mu.Unlock()
	return sess, nil
}

// fileTimestamp turns an RFC3339Nano timestamp into a filesystem-safe
// prefix: ":" and "." → "-". Mirrors upstream's filename convention.
func fileTimestamp(ts string) string {
	r := strings.NewReplacer(":", "-", ".", "-")
	return r.Replace(ts)
}

// Load reads and migrates a session JSONL and makes it current. A missing path starts a fresh session at that exact path, with persistence deferred until the first user or assistant message.
func (sm *SessionManager) Load(path string) (*Session, error) {
	sess, err := loadSessionFile(path)
	if errors.Is(err, os.ErrNotExist) {
		resolved, resolveErr := ResolvePath(path, "")
		if resolveErr != nil {
			return nil, resolveErr
		}
		id, idErr := generateSessionID()
		if idErr != nil {
			return nil, idErr
		}
		sess = NewSession(id, sm.cwd)
		sess.path = resolved
		err = nil
	}
	if err != nil {
		return nil, err
	}
	sess.sessionDir = sm.sessionDir
	sm.mu.Lock()
	sm.current = sess
	sm.mu.Unlock()
	return sess, nil
}

// loadSessionFile parses and migrates a JSONL session. It rejects empty files and files without a session header.
func loadSessionFile(path string) (*Session, error) {
	resolvedPath, err := ResolvePath(path, "")
	if err != nil {
		return nil, fmt.Errorf("session: resolve %s: %w", path, err)
	}
	path = resolvedPath
	records, err := LoadEntriesFromFile(path)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("session: open %s: %w", path, err)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("session: empty file: %s", path)
		}
		return nil, fmt.Errorf("Session file is not a valid pi session: %s", path)
	}
	return restoreSessionFileEntries(path, records)
}

// Current returns the active session.
func (sm *SessionManager) Current() *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// SessionDir returns the session directory.
func (sm *SessionManager) SessionDir() string { return sm.sessionDir }

// CWD returns the working directory.
func (sm *SessionManager) CWD() string { return sm.cwd }

// Clone extracts the chosen root-to-leaf path with resolved labels into a fresh Session. In-memory sessions stay in memory. A persisted clone is written immediately only when its path contains a user or assistant message; otherwise its first such message creates the file.
func (sm *SessionManager) Clone(source *Session, leafID string) (*Session, error) {
	if source == nil {
		return nil, fmt.Errorf("sessionmanager: Clone: nil source")
	}
	chain := source.Branch(leafID)
	if len(chain) == 0 {
		return nil, fmt.Errorf("Entry %s not found", leafID)
	}

	records, err := branchedSessionEntries(source, chain)
	if err != nil {
		return nil, err
	}
	newID, err := generateSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	header := SessionHeader{
		Type:          "session",
		Version:       CurrentSessionVersion,
		ID:            newID,
		Timestamp:     now,
		CWD:           sm.cwd,
		ParentSession: source.path,
	}
	headerJSON, err := marshalSessionLine(header)
	if err != nil {
		return nil, err
	}
	records = append([]json.RawMessage{headerJSON}, records...)
	loaded, err := newSessionFromEntries(sm.cwd, newID, records)
	if err != nil {
		return nil, err
	}
	if source.path != "" {
		if err := os.MkdirAll(sm.sessionDir, 0o755); err != nil {
			return nil, err
		}
		loaded.path = filepath.Join(sm.sessionDir, fileTimestamp(now)+"_"+newID+".jsonl")
		loaded.sessionDir = sm.sessionDir
		if loaded.hasConversation {
			lines := make([][]byte, len(records))
			for i, raw := range records {
				lines[i] = raw
			}
			if err := writeSessionLines(loaded.path, lines); err != nil {
				return nil, err
			}
			loaded.flushed = true
		}
	}
	sm.mu.Lock()
	sm.current = loaded
	sm.mu.Unlock()
	return loaded, nil
}

// ForkFromFile copies every non-header entry from sourcePath into a new JSONL. An optional ID is validated and used verbatim; omission generates a UUIDv7. The header records the absolute source path as parentSession. Unlike Clone, this preserves the complete entry tree and writes the file immediately.
func (sm *SessionManager) ForkFromFile(sourcePath string, idOption ...string) (*Session, error) {
	abs, err := ResolvePath(sourcePath, "")
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: fork: bad path: %w", err)
	}
	// Pi session-manager.ts forkFrom validates the source with loadEntriesFromFile, which reads a missing, empty, or headerless file as no entries.
	records, err := LoadEntriesFromFile(abs)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("Cannot fork: source session file is empty or invalid: %s", abs)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: fork read: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var sourceHeader SessionHeader
	if err := json.Unmarshal([]byte(lines[0]), &sourceHeader); err != nil || sourceHeader.Type != "session" {
		return nil, fmt.Errorf("Cannot fork: source session has no header: %s", abs)
	}

	var option *string
	if len(idOption) > 0 {
		option = &idOption[0]
	}
	created, err := newSessionWithOptions(sm.cwd, option, abs)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sm.sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionmanager: mkdir: %w", err)
	}
	header := created.Header()
	newPath := filepath.Join(sm.sessionDir, fileTimestamp(header.Timestamp)+"_"+header.ID+".jsonl")

	f, err := os.Create(newPath)
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: fork create: %w", err)
	}
	defer func() { _ = f.Close() }()
	headerJSON, _ := json.Marshal(header)
	if _, err := fmt.Fprintf(f, "%s\n", headerJSON); err != nil {
		return nil, err
	}
	// Copy every original entry verbatim, skipping the source header so
	// the new file has exactly one header. Entry IDs are preserved,
	// matching upstream forkFrom.
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err == nil && probe.Type == "session" {
			continue
		}
		if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
			return nil, err
		}
	}

	loaded, err := loadSessionFile(newPath)
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: fork reload: %w", err)
	}
	sm.mu.Lock()
	sm.current = loaded
	sm.mu.Unlock()
	return loaded, nil
}

// DeleteSession deletes a session file within the listed root, trying trash before unlink. A file that is already gone counts as success, as in the selector.
func (sm *SessionManager) DeleteSession(path string) error {
	result := sm.deleteListedSession(path)
	if !result.ok {
		return errors.New(result.error)
	}
	return nil
}

// deleteListedSession deletes a session file the selector listed with Pi's deleteSessionFile. Pi's selector deletes any confirmed path; PiG refuses a path outside the directory the All scope lists, comparing whole path elements (case-insensitively on Windows), so every listed session remains deletable.
func (sm *SessionManager) deleteListedSession(path string) sessionDeleteResult {
	refuse := func(message string) sessionDeleteResult {
		return sessionDeleteResult{method: sessionDeleteUnlink, error: message}
	}
	absPath, err := nodepath.Resolve(path)
	if err != nil {
		return refuse("sessionmanager: delete: bad path: " + err.Error())
	}
	absRoot, err := nodepath.Resolve(sm.sessionListRoot())
	if err != nil {
		return refuse("sessionmanager: delete: bad dir: " + err.Error())
	}
	if !pathWithin(absPath, absRoot) {
		return refuse("sessionmanager: refusing to delete file outside session dir")
	}
	return deleteSessionFile(path)
}

// RenameSession updates the session name by appending a session_info entry.
// Mirrors upstream session-selector.ts rename functionality.
func (sm *SessionManager) RenameSession(path, newName string) error {
	session, err := loadSessionFile(path)
	if err != nil {
		return fmt.Errorf("sessionmanager: rename open: %w", err)
	}
	_, err = session.AppendSessionInfo(newName)
	return err
}

// forEachJSONLLine calls fn with each line of r without its line ending. Lines
// have no length limit, as upstream reads whole session files. The slice is
// valid only during the call. A read error is wrapped; an fn error is
// returned as is.
func forEachJSONLLine(r io.Reader, fn func(line []byte) error) error {
	return forEachJSONLLineContext(context.Background(), r, fn)
}

func forEachJSONLLineContext(ctx context.Context, r io.Reader, fn func(line []byte) error) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var long []byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, chunk...)
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("session: read: %w", err)
		}
		line := chunk
		if len(long) > 0 {
			long = append(long, chunk...)
			line = long
		}
		if len(line) > 0 {
			line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
			if fnErr := fn(line); fnErr != nil {
				return fnErr
			}
		}
		long = long[:0]
		if err != nil {
			return nil
		}
	}
}
