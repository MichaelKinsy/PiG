package codingagent

// Ports packages/coding-agent/src/core/session-manager.ts.

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

// NewSession resets this manager to an empty Session with the same persistence mode and directory.
func (s *Session) NewSession(parentSession string) error {
	id, err := GenerateSessionID()
	if err != nil {
		return err
	}
	next := NewSession(id, s.CWD())
	if s.IsPersisted() {
		next, err = NewSessionManagerWithDir(s.CWD(), s.GetSessionDir()).Create(id, parentSession)
		if err != nil {
			return err
		}
	} else {
		next.header.ParentSession = parentSession
	}
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
	s.msgCache = nil
	s.undecodable = nil
	s.msgMu.Unlock()
	s.mu.Unlock()
}
