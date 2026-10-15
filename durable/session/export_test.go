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
