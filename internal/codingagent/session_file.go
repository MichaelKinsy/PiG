// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// FileEntry is one record of a session file: the header (SessionHeader) or an entry (SessionEntry) (session-manager.ts FileEntry).
type FileEntry = sessionentry.FileEntry

// ParseSessionEntries splits JSONL content into its records, in order. Ports packages/coding-agent/src/core/session-manager.ts (parseSessionEntries): the content is trimmed and split on "\n", a line that is not valid JSON, including a blank one, is skipped (only JSON whitespace may surround a record, so a line led by U+00A0 is skipped). Unlike [LoadEntriesFromFile] it keeps a record whose JSON value is falsy (null, false, 0, ""), because Pi pushes whatever JSON.parse returns.
func ParseSessionEntries(content string) []FileEntry {
	entries := []FileEntry{}
	//portlint:allow pathseparators Pi splits on "\n" alone (session-manager.ts parseSessionEntries: content.trim().split("\n")); a CRLF record keeps its \r and the JSON parse accepts it
	for line := range strings.SplitSeq(widthx.JSTrim(content), "\n") {
		if json.Valid([]byte(line)) {
			entries = append(entries, sessionentry.DecodeFileEntry(json.RawMessage(strings.Trim(line, " \t\r\n"))))
		}
	}
	return entries
}

// Ports packages/coding-agent/src/core/session-manager.ts (loadEntriesFromFile).
// LoadEntriesFromFile skips malformed and JSON-falsy lines and requires a session header before accepting records.
func LoadEntriesFromFile(path string) ([]FileEntry, error) {
	records, unterminated, err := readSessionFileEntries(path)
	if err != nil {
		return nil, err
	}
	if unterminated {
		if err := appendSessionLine(path, nil); err != nil {
			return nil, err
		}
	}
	return records, nil
}

type sessionTailReader struct {
	io.Reader
	last byte
	read bool
}

func (r *sessionTailReader) Read(buffer []byte) (int, error) {
	n, err := r.Reader.Read(buffer)
	if n > 0 {
		r.last = buffer[n-1]
		r.read = true
	}
	return n, err
}

func truthySessionLine(line []byte) bool {
	line = bytes.TrimSpace(line)
	if !json.Valid(line) {
		return false
	}
	switch line[0] {
	case '{', '[', 't':
		return true
	case 'n', 'f':
		return false
	case '"':
		var value string
		if json.Unmarshal(line, &value) != nil {
			return false
		}
		return value != ""
	default:
		number, err := strconv.ParseFloat(string(line), 64)
		return (err == nil || errors.Is(err, strconv.ErrRange)) && number != 0
	}
}

func readSessionFileEntries(path string) ([]FileEntry, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []FileEntry{}, false, nil
		}
		return nil, false, nodeerrno.FromPathError(err)
	}
	defer func() { _ = file.Close() }()
	reader := &sessionTailReader{Reader: file}
	records := []FileEntry{}
	err = forEachJSONLLine(reader, func(line []byte) error {
		if truthySessionLine(line) {
			records = append(records, sessionentry.DecodeFileEntry(bytes.Clone(line)))
		}
		return nil
	})
	if err != nil {
		// readSync's error: "EISDIR: illegal operation on a directory, read".
		if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
			err = nodeerrno.FromPathError(pathErr)
		}
		return nil, false, err
	}
	if len(records) == 0 {
		return records, false, nil
	}
	if _, valid := records[0].(SessionHeader); !valid {
		return []FileEntry{}, false, nil
	}
	return records, reader.read && reader.last != '\n', nil
}

// Open opens an explicit session file, with an optional effective working-directory override.
func (sm *SessionManager) Open(path string, cwdOverride ...string) (*Session, error) {
	resolved, err := ResolvePath(path, "")
	if err != nil {
		return nil, err
	}
	records, err := LoadEntriesFromFile(resolved)
	if err != nil {
		return nil, err
	}
	var session *Session
	if len(records) == 0 {
		info, statErr := os.Stat(resolved)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
		if statErr == nil && info.Size() > 0 {
			return nil, fmt.Errorf("Session file is not a valid pi session: %s", resolved)
		}
		cwd := sm.cwd
		if len(cwdOverride) > 0 {
			cwd = cwdOverride[0]
		}
		cwd = resolveSessionCWD(cwd)
		session, err = newSessionWithOptions(cwd, nil, "")
		if err != nil {
			return nil, err
		}
		session.path = resolved
		if statErr == nil {
			header, err := marshalSessionLine(session.GetHeader())
			if err != nil {
				return nil, err
			}
			if err := writeSessionLines(resolved, [][]byte{header}); err != nil {
				return nil, err
			}
			session.flushed = true
		}
	} else {
		session, err = restoreSessionFileEntries(resolved, records)
		if err != nil {
			return nil, err
		}
		cwd := session.header.CWD
		if cwd == "" {
			cwd = sm.cwd
		}
		if len(cwdOverride) > 0 {
			cwd = cwdOverride[0]
		}
		cwd = resolveSessionCWD(cwd)
		session.effectiveCWD = &cwd
	}
	session.sessionDir = sm.sessionDir
	sm.mu.Lock()
	sm.current = session
	sm.mu.Unlock()
	return session, nil
}

// resolveSessionCWD resolves an opened session's effective cwd as Pi's
// SessionManager constructor does: resolvePath (tilde and file URL
// normalization, then path.resolve) with no realpath, so a symlinked
// directory such as macOS /var stays as given.
// upstream: packages/coding-agent/src/core/session-manager.ts:SessionManager.constructor
func resolveSessionCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	if resolved, err := ResolvePath(cwd, ""); err == nil {
		return resolved
	}
	return absCleanDir(cwd)
}

func restoreSessionFileEntries(path string, records []FileEntry) (*Session, error) {
	return restoreSessionEntries(path, records, true)
}

// restoreSessionEntries builds the session of a loaded file. persist writes the migrated file back, as upstream _rewriteFile does only for a persisting manager.
func restoreSessionEntries(path string, records []FileEntry, persist bool) (*Session, error) {
	header, valid := records[0].(SessionHeader)
	if !valid {
		return nil, fmt.Errorf("Session file is not a valid pi session: %s", path)
	}
	session, err := newSessionFromEntries(header.CWD, header.ID, records)
	if err != nil {
		return nil, err
	}
	session.path, session.flushed = path, true
	if persist && header.Version < CurrentSessionVersion {
		headerRaw, err := replaceJSONField(records[0].Raw(), "version", CurrentSessionVersion)
		if err != nil {
			return nil, err
		}
		lines := make([][]byte, 0, len(session.entries)+1)
		lines = append(lines, headerRaw)
		for _, entry := range session.entries {
			lines = append(lines, entry.Raw())
		}
		if err := writeSessionLines(path, lines); err != nil {
			return nil, err
		}
	}
	return session, nil
}
