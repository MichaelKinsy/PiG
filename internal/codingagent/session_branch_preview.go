package codingagent

// NewBranchSession returns an in-memory session that holds header's identity and the entries of branch, root first, as they are: each entry keeps its id and parent link, and the session shares the entry values with the session they came from instead of encoding and decoding each record again, as agent-session.ts _createBoundaryPreviewManager hands SessionManager.inMemory the branch's own objects.
// The session serves reads of the branch's projection and further appends (a boundary preview appends its drafts). It keeps no statistics or name of the entries, which only a session that is saved or listed reads.
func NewBranchSession(header SessionHeader, branch []SessionEntry) *Session {
	s := NewSession(header.ID, header.CWD)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range branch {
		base := entry.Base()
		// Only a compaction and a label read their record when they join the log; a message entry encodes its record on each read.
		var raw []byte
		if base.Type == typeCompaction || base.Type == typeLabel {
			raw = entry.Raw()
		}
		idx := s.addSlotLocked(newSlot(entry), base.ParentID, raw)
		s.leafID = leafOf(base.ID)
		s.revision++
		s.advanceBranchLocked(idx)
	}
	return s
}
