package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// notifyOAuthSignalRelease (ext→host) reports that the extension no longer holds the AbortSignal it received with request_id, so the host stops forwarding the caller's abort to it.
const notifyOAuthSignalRelease = "oauth.signal_release"

// OAuthSignalReleaseArgs is the Args of notifyOAuthSignalRelease.
type OAuthSignalReleaseArgs struct {
	RequestID string `json:"request_id"`
}

// abortForwards carries a caller's abort to an AbortSignal that an extension retained after its request settled. Pi's refresh signal is AbortSignal.any([caller, ...]): it follows the caller for as long as the extension holds it (auth/resolve.ts:149-153). While a request is in flight the connection's own cancellation covers this; a settled request needs a cancel frame for its retained signal.
type abortForwards struct {
	mu     sync.Mutex
	stops  map[string]func() bool
	closed bool
}

// forward sends a cancel for requestID when signalCtx ends, until the extension releases the signal or the connection closes.
func (c *Conn) forwardAbort(signalCtx context.Context, requestID string) {
	if signalCtx.Done() == nil {
		return
	}
	f := &c.abortForwards
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	if f.stops == nil {
		f.stops = make(map[string]func() bool)
	}
	f.stops[requestID] = context.AfterFunc(signalCtx, func() {
		f.mu.Lock()
		delete(f.stops, requestID)
		f.mu.Unlock()
		if c.closed.Load() {
			return
		}
		_ = c.Send(&Envelope{Type: MsgCancel, ID: requestID, Cancel: &CancelPayload{RequestID: requestID, Reason: signalCtx.Err().Error()}})
	})
}

// releaseAbort stops forwarding for requestID.
func (c *Conn) releaseAbort(requestID string) {
	f := &c.abortForwards
	f.mu.Lock()
	stop := f.stops[requestID]
	delete(f.stops, requestID)
	f.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// stopAbortForwards ends every forward when the connection closes.
func (c *Conn) stopAbortForwards() {
	f := &c.abortForwards
	f.mu.Lock()
	f.closed = true
	stops := f.stops
	f.stops = nil
	f.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
}

func (c *Conn) handleOAuthSignalRelease(args json.RawMessage) {
	var release OAuthSignalReleaseArgs
	if json.Unmarshal(args, &release) == nil && release.RequestID != "" {
		c.releaseAbort(release.RequestID)
	}
}

// newRequestID returns a request ID unique on this connection.
func (c *Conn) newRequestID() string { return fmt.Sprintf("r%d", c.nextID.Add(1)) }
