// Ports packages/coding-agent/src/core/session-manager.ts.

package codingagent

import (
	"path/filepath"
)

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

// UsesDefaultSessionDir reports whether the session directory is the default one for the session's working directory
// (session-manager.ts usesDefaultSessionDir). An in-memory manager has no session directory.
func (s *Session) UsesDefaultSessionDir() bool {
	return s.sessionDir != "" && s.sessionDir == defaultSessionDir(s.CWD())
}

// GetLeafEntry returns the entry at the current leaf, or false when there is none (session-manager.ts getLeafEntry).
func (s *Session) GetLeafEntry() (SessionEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return nil, false
	}
	idx, ok := s.slotOf(*s.leafID)
	if !ok {
		return nil, false
	}
	return s.entryAtLocked(idx), true
}

// ResetLeaf moves the leaf before the first entry, so the next append creates a root (session-manager.ts resetLeaf).
func (s *Session) ResetLeaf() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leafID = nil
	s.revision++
}

// GetChildren returns the direct children of an entry in append order (session-manager.ts getChildren).
func (s *Session) GetChildren(parentID string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var children []int32
	for i := range s.slots {
		if parent, has := s.parentIDLocked(i); has && parent == parentID {
			children = append(children, int32(i))
		}
	}
	if children == nil {
		return nil
	}
	return s.entriesAtLocked(children)
}

// GetLabel returns the label most recently set on an entry; a later empty or nil label clears it (session-manager.ts getLabel).
func (s *Session) GetLabel(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	label := s.labels[id].label
	return label, label != ""
}
