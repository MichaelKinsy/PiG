// Ports packages/coding-agent/src/core/session-manager.ts.

package codingagent

import "path/filepath"

// GetCwd returns the working directory recorded by this manager.
func (s *Session) GetCwd() string { return s.CWD() }

// GetSessionId returns the current log identity.
func (s *Session) GetSessionId() string { return s.ID() }

// GetSessionFile returns the selected persistence path, or nil for an in-memory manager. A selected file need not have been flushed yet.
func (s *Session) GetSessionFile() *string {
	if path := s.Path(); path != "" {
		return new(path)
	}
	return nil
}

// GetSessionDir returns the configured session directory. A directly loaded log without an override uses its file's parent; an in-memory manager has no directory.
func (s *Session) GetSessionDir() string {
	if s.sessionDir != "" {
		return s.sessionDir
	}
	if path := s.Path(); path != "" {
		return filepath.Dir(path)
	}
	return ""
}

// IsPersisted reports whether the manager has a persistence path, independently of the first assistant-triggered flush.
func (s *Session) IsPersisted() bool { return s.Path() != "" }
