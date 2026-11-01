package session

// IsClosing reports whether Close began.
func IsClosing(session *SessionImpl) bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.closing != nil
}

// IsSealed reports whether the transaction's callback settled.
func IsSealed(tx *Transaction) bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.sealed
}

// CloseSubscriptions reports the number of live close subscriptions.
func CloseSubscriptions(session *SessionImpl) int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.closeOrder)
}

// QueuedParts is the number of parts admitted behind the call in flight, not yet sent.
func QueuedParts(session *SessionImpl) int { return session.writer.Queued() }

// AwaitingResumptions is the number of acknowledgements the writer or the delivery goroutine waits for now: callers released
// and waits readied that have not resumed yet.
func AwaitingResumptions(session *SessionImpl) int { return int(session.awaiting.Load()) }

// ParkedWaits is the number of waits registered (Park) that have not resumed.
func ParkedWaits(session *SessionImpl) int {
	session.parkMu.Lock()
	defer session.parkMu.Unlock()
	return len(session.parks)
}
