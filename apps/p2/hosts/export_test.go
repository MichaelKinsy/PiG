package hosts

import "context"

// StartedForTest is started, for a test of the wait itself.
func (w *SessionWorkers) StartedForTest(ctx context.Context, session string, began bool) error {
	return w.started(ctx, session, began)
}
