// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	return filepath.Join(AgentDir(), "sessions", encodeCwdForSessionDir(cwd))
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

// Create creates a new session file on disk. parentSessionPath is
// non-empty only when this Create was invoked by Clone: it goes into
// the new file's header as `parentSession`.
func (sm *SessionManager) Create(id, parentSessionPath string) (*Session, error) {
	if err := os.MkdirAll(sm.sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionmanager: mkdir: %w", err)
	}
	sess := NewSession(id, sm.cwd)
	if parentSessionPath != "" {
		sess.header.ParentSession = parentSessionPath
	}
	// File name: <ts>_<id>.jsonl (matches upstream ordering convention
	// so directory listings sort chronologically).
	filename := fileTimestamp(sess.header.Timestamp) + "_" + id + ".jsonl"
	path := filepath.Join(sm.sessionDir, filename)
	sess.path = path

	// Do not write the file yet. Mirrors upstream SessionManager.newSession,
	// which sets the path but defers the first disk write to _persist on the
	// first assistant message. This keeps abandoned sessions (opened but
	// never answered) off disk instead of leaving empty .jsonl files.

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

// Load reads a session JSONL into memory and makes it the current session.
func (sm *SessionManager) Load(path string) (*Session, error) {
	sess, err := loadSessionFile(path)
	if err != nil {
		return nil, err
	}
	sm.mu.Lock()
	sm.current = sess
	sm.mu.Unlock()
	return sess, nil
}

// loadSessionFile parses a JSONL file from disk. Returns an error if
// the file is empty or has no header.
func loadSessionFile(path string) (*Session, error) {
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("session: resolve %s: %w", path, err)
	}
	path = resolvedPath
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sess := &Session{path: path, byID: make(map[string]SessionEntry), flushed: true}
	first := true
	err = forEachJSONLLine(f, func(line []byte) error {
		if len(line) == 0 {
			return nil
		}
		if first {
			first = false
			if err := json.Unmarshal(line, &sess.header); err != nil {
				return fmt.Errorf("session: parse header: %w", err)
			}
			if sess.header.Type != "session" {
				return fmt.Errorf("session: missing header in %s", path)
			}
			return nil
		}
		var base SessionEntryBase
		// upstream: coding-agent/src/core/session-manager.ts:parseSessionEntryLine
		if err := json.Unmarshal(line, &base); err != nil {
			return nil // malformed entry: skip but keep parsing the rest
		}
		// Session accounting is derived while the file is already being scanned so /session does no history-sized work on the input loop.
		base.Type = sess.stats.add(line, base.Type)
		buf := make([]byte, len(line))
		copy(buf, line)
		se := SessionEntry{raw: buf, Base: base}
		sess.entries = append(sess.entries, se)
		sess.byID[base.ID] = se
		if base.ID != "" {
			id := base.ID
			sess.leafID = &id
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if first {
		return nil, fmt.Errorf("session: empty file: %s", path)
	}
	return sess, nil
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

// Clone snapshots the path-to-leafID from `source` and writes it as a
// brand-new JSONL with a fresh session ID. The new file's header has
// `parentSession` pointing at the source path. The cloned session
// becomes the manager's current session.
//
// The cloned file contains a LINEAR chain (no branches): all entries
// off the chosen leaf path are dropped. This is what makes /clone
// useful: it produces a self-contained "best path" sub-session.
//
// Mirrors upstream `forkFrom` + `createBranchedSession`.
func (sm *SessionManager) Clone(source *Session, leafID string) (*Session, error) {
	if source == nil {
		return nil, fmt.Errorf("sessionmanager: Clone: nil source")
	}
	chain := source.Branch(leafID)
	if len(chain) == 0 {
		return nil, fmt.Errorf("sessionmanager: Clone: leaf %q not found in source", leafID)
	}

	if err := os.MkdirAll(sm.sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionmanager: mkdir: %w", err)
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
	filename := fileTimestamp(now) + "_" + newID + ".jsonl"
	newPath := filepath.Join(sm.sessionDir, filename)

	f, err := os.Create(newPath)
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: clone create: %w", err)
	}
	defer func() { _ = f.Close() }()
	headerJSON, _ := json.Marshal(header)
	if _, err := fmt.Fprintf(f, "%s\n", headerJSON); err != nil {
		return nil, err
	}
	for _, e := range chain {
		if _, err := fmt.Fprintf(f, "%s\n", e.raw); err != nil {
			return nil, err
		}
	}

	loaded, err := loadSessionFile(newPath)
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: clone reload: %w", err)
	}
	sm.mu.Lock()
	sm.current = loaded
	sm.mu.Unlock()
	return loaded, nil
}

// ForkFromFile copies every non-header entry from sourcePath into a
// brand-new JSONL with a fresh session ID whose header records
// `parentSession` = the absolute source path. Unlike Clone (which keeps
// only the linear path to a chosen leaf), this preserves the full entry
// tree, matching upstream SessionManager.forkFrom used by `pig --fork`.
// The new file is written to disk immediately and becomes the manager's
// current session.
func (sm *SessionManager) ForkFromFile(sourcePath string) (*Session, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: fork: bad path: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("sessionmanager: fork read: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("sessionmanager: fork: source session is empty: %s", abs)
	}
	var sourceHeader SessionHeader
	if err := json.Unmarshal([]byte(lines[0]), &sourceHeader); err != nil || sourceHeader.Type != "session" {
		return nil, fmt.Errorf("sessionmanager: fork: source session has invalid header")
	}

	if err := os.MkdirAll(sm.sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionmanager: mkdir: %w", err)
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
		ParentSession: abs,
	}
	newPath := filepath.Join(sm.sessionDir, fileTimestamp(now)+"_"+newID+".jsonl")

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

// DeleteSession removes a session file from disk.
// Returns error if the path doesn't exist or can't be removed.
// Mirrors upstream session-selector.ts delete functionality.
func (sm *SessionManager) DeleteSession(path string) error {
	// Safety check: only delete files in the session directory.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("sessionmanager: delete: bad path: %w", err)
	}
	absDir, err := filepath.Abs(sm.sessionDir)
	if err != nil {
		return fmt.Errorf("sessionmanager: delete: bad dir: %w", err)
	}
	if !strings.HasPrefix(absPath, absDir) {
		return fmt.Errorf("sessionmanager: refusing to delete file outside session dir")
	}
	return os.Remove(path)
}

// RenameSession updates the session name by appending a session_info entry.
// Mirrors upstream session-selector.ts rename functionality.
func (sm *SessionManager) RenameSession(path, newName string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("sessionmanager: rename open: %w", err)
	}
	defer func() { _ = f.Close() }()

	id, err := generateSessionID()
	if err != nil {
		return err
	}
	entry := SessionInfoEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "session_info",
			ID:        id,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		Name: newName,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// forEachJSONLLine calls fn with each line of r without its line ending. Lines
// have no length limit, as upstream reads whole session files. The slice is
// valid only during the call. A read error is wrapped; an fn error is
// returned as is.
func forEachJSONLLine(r io.Reader, fn func(line []byte) error) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var long []byte
	for {
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
