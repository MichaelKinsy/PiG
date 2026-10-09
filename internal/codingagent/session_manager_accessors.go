// Ports packages/coding-agent/src/core/session-manager.ts.

package codingagent

import (
	"encoding/json"
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
	entry, ok := s.byID[*s.leafID]
	return entry, ok
}

// ResetLeaf moves the leaf before the first entry, so the next append creates a root (session-manager.ts resetLeaf).
func (s *Session) ResetLeaf() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leafID = nil
}

// GetChildren returns the direct children of an entry in append order (session-manager.ts getChildren).
func (s *Session) GetChildren(parentID string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var children []SessionEntry
	for _, entry := range s.entries {
		if entry.Base().ParentID != nil && *entry.Base().ParentID == parentID {
			children = append(children, entry)
		}
	}
	return children
}

// GetLabel returns the label most recently set on an entry; a later empty or nil label clears it (session-manager.ts getLabel).
func (s *Session) GetLabel(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var label string
	for _, entry := range s.entries {
		if entry.Base().Type != "label" {
			continue
		}
		var parsed LabelEntry
		if json.Unmarshal(entry.Raw(), &parsed) != nil || parsed.TargetID != id {
			continue
		}
		label = ""
		if parsed.Label != nil {
			label = *parsed.Label
		}
	}
	return label, label != ""
}
