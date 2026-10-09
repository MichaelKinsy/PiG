package codingagent

// Ports packages/coding-agent/src/core/session-manager.ts.

import (
	"fmt"
	"os"
)

// CreateBranchedSession replaces this manager's active log with a new branch while retaining the manager object. A memory-only manager does not acquire a persistence path.
func (s *Session) CreateBranchedSession(leafID string) (string, error) {
	manager := NewSessionManagerWithDir(s.CWD(), s.GetSessionDir())
	branched, err := manager.Clone(s, leafID)
	if err != nil {
		return "", err
	}
	s.replaceSessionLog(branched)
	return s.Path(), nil
}

// NewSession resets this manager to an empty Session with the same persistence mode and directory, and returns the new
// session file path (empty when the session is not persisted). An ID that is not a valid session id is an error and
// leaves the manager unchanged.
// Mirrors upstream SessionManager.newSession (core/session-manager.ts:1057).
func (s *Session) NewSession(options *NewSessionOptions) (string, error) {
	if options == nil {
		options = &NewSessionOptions{}
	}
	var next *Session
	var err error
	if s.IsPersisted() {
		id := ""
		if options.ID != nil {
			id = *options.ID
			if !sessionIDPattern.MatchString(id) {
				_, err = newSessionWithOptions(s.CWD(), options.ID, options.ParentSession)
				return "", err
			}
		} else if id, err = GenerateSessionID(); err != nil {
			return "", err
		}
		next, err = NewSessionManagerWithDir(s.CWD(), s.GetSessionDir()).Create(id, options.ParentSession)
	} else {
		next, err = newSessionWithOptions(s.CWD(), options.ID, options.ParentSession)
	}
	if err != nil {
		return "", err
	}
	s.replaceSessionLog(next)
	return s.Path(), nil
}

// SetSessionFile switches this manager to the session file at path, as upstream SessionManager.setSessionFile does for resume and branching. The manager keeps its cwd and session directory. A file with entries is loaded as flushed; an empty file starts a new session that a persisting manager writes as a header; a missing path starts a new session that keeps the explicit path until its first user or assistant message. Upstream assigns the session file before it reads the file, so a non-empty file that is not a session is an error after which a persisting manager writes to that path while its entries stay unchanged. A manager that does not persist reads the file but never writes it and keeps no session file.
// Mirrors upstream SessionManager.setSessionFile and _setSessionFile (core/session-manager.ts:1025-1054).
func (s *Session) SetSessionFile(path string) error {
	resolved, err := ResolvePath(path, "")
	if err != nil {
		return err
	}
	persisted := s.IsPersisted()
	cwd := s.CWD()
	fail := func(err error) error {
		if persisted {
			s.mu.Lock()
			s.path = resolved
			s.mu.Unlock()
		}
		return err
	}
	var next *Session
	// existsSync is false for any stat failure, which upstream treats as a missing file.
	if info, statErr := os.Stat(resolved); statErr == nil {
		records, err := LoadEntriesFromFile(resolved)
		if err != nil {
			return fail(err)
		}
		if len(records) == 0 {
			if info.Size() > 0 {
				return fail(fmt.Errorf("Session file is not a valid pi session: %s", resolved))
			}
			if next, err = newSessionWithOptions(cwd, nil, ""); err != nil {
				return fail(err)
			}
			if persisted {
				header, err := marshalSessionLine(next.GetHeader())
				if err != nil {
					return fail(err)
				}
				if err := writeSessionLines(resolved, [][]byte{header}); err != nil {
					return fail(err)
				}
			}
			next.flushed = true
		} else {
			if next, err = restoreSessionEntries(resolved, records, persisted); err != nil {
				return fail(err)
			}
			next.effectiveCWD = &cwd
		}
	} else if next, err = newSessionWithOptions(cwd, nil, ""); err != nil {
		return fail(err)
	}
	next.path = resolved
	if !persisted {
		next.path = ""
	}
	next.sessionDir = s.GetSessionDir()
	s.replaceSessionLog(next)
	return nil
}

func (s *Session) replaceSessionLog(branched *Session) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	s.mu.Lock()
	s.msgMu.Lock()
	s.header = branched.header
	s.effectiveCWD = branched.effectiveCWD
	s.entries = branched.entries
	s.byID = branched.byID
	s.path = branched.path
	s.sessionDir = branched.sessionDir
	s.leafID = branched.leafID
	s.flushed = branched.flushed
	s.hasConversation = branched.hasConversation
	s.stats = branched.stats
	s.undecodable = nil
	s.msgMu.Unlock()
	s.mu.Unlock()
}
