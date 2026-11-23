package session

import "github.com/MichaelKinsy/PiG/durable"

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

// AwaitingCallers is the number of released commit callers the delivery goroutine waits for now, to have resumed.
func AwaitingCallers(session *SessionImpl) int { return int(session.awaitingCallers.Load()) }

// AwaitingWakes is the number of registered waits readied by a publication's wakes that the writer waits for now, to have resumed.
func AwaitingWakes(session *SessionImpl) int { return int(session.awaitingWakes.Load()) }

// ParkedWaits is the number of waits registered (Park) that have not resumed.
func ParkedWaits(session *SessionImpl) int {
	session.parkMu.Lock()
	defer session.parkMu.Unlock()
	return len(session.parks)
}

// WrapWriterHost replaces the host the Session's writer calls by wrap(host), once the writer is idle. A test makes one step
// of a publication fail through it where Pi's test patches a prototype method (CachedStorage.prototype.published,
// Transaction.prototype.adopt, Transaction.prototype.adoptHeld), which Go cannot.
func WrapWriterHost(session *SessionImpl, wrap func(WriterHost) WriterHost) {
	session.writer.Wait()
	session.writer.host = wrap(session.writer.host)
}

// AbortPrepared aborts the changes tx prepared, so memory cannot adopt its commit: Transaction.adopt and adoptHeld fail.
func AbortPrepared(tx *Transaction) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	for _, document := range tx.documents {
		if document.change != nil {
			document.change.Abort()
		}
	}
}

// SetAfterSignalRead sets the function each signal delivery of tx calls once it read its task and took its place in line,
// before it waits for its turn: a test parks the deliveries there to run them in an order of its own.
func SetAfterSignalRead(tx *Transaction, after func(durable.TaskId, string, durable.JsonValue)) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.afterSignalRead = after
}

// SetAfterParkEnter sets the function Park calls immediately after enter(continuationOf(ctx)).
// Must be called before any goroutine can call Park on session (unsynchronised read in Park).
func SetAfterParkEnter(session *SessionImpl, after func()) {
	session.afterParkEnter = after
}
