package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// pig additive (D19): Pi has one process-wide withFileMutationQueue (core/tools/file-mutation-queue.ts) that the built-in edit and write tools and every
// extension share. An extension process of another SDK, or a Node process of its own, has no access to the host's map, so each SDK asks the host's queue for its
// turn instead (CallFileMutationEnter, CallFileMutationExit) and keeps no queue of its own.

// connFileMutations are the tickets one connection holds. A ticket belongs to the connection that was admitted: when the connection closes (the extension exited,
// was replaced, or the host shut down) every ticket it still holds is released, so a crashed extension cannot keep a file blocked.
type connFileMutations struct {
	mu     sync.Mutex
	next   uint64
	closed bool
	held   map[string]*tools.MutationTicket
}

// admit records a ticket the extension was admitted to and returns its id; false when the connection already closed, in which case the caller releases the ticket.
func (c *connFileMutations) admit(ticket *tools.MutationTicket) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", false
	}
	if c.held == nil {
		c.held = make(map[string]*tools.MutationTicket)
	}
	c.next++
	id := fmt.Sprintf("fm-%d", c.next)
	c.held[id] = ticket
	return id, true
}

// take removes and returns the ticket id names.
func (c *connFileMutations) take(id string) (*tools.MutationTicket, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticket, ok := c.held[id]
	delete(c.held, id)
	return ticket, ok
}

// releaseAll releases every held ticket and refuses later admissions.
func (c *connFileMutations) releaseAll() {
	c.mu.Lock()
	c.closed = true
	held := c.held
	c.held = nil
	c.mu.Unlock()
	for _, ticket := range held {
		ticket.Release()
	}
}

// handleFileMutationEnter serves CallFileMutationEnter: it takes the path's next place in the host's queue before the call lane advances (extension.CallInitiated),
// then waits for the earlier mutations to complete.
func (h *Host) handleFileMutationEnter(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var args FileMutationEnterArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		extension.CallInitiated(ctx)
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	ticket, err := tools.ReserveFileMutation(args.Path)
	extension.CallInitiated(ctx)
	if err != nil {
		return nil, err
	}
	if err := ticket.WaitContext(ctx); err != nil {
		return nil, err
	}
	id, ok := conn.fileMutations.admit(ticket)
	if !ok {
		ticket.Release()
		return nil, errors.New("extension connection closed")
	}
	result, err := json.Marshal(FileMutationEnterResult{Ticket: id})
	if err != nil {
		if released, held := conn.fileMutations.take(id); held {
			released.Release()
		}
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}

// handleFileMutationExit serves CallFileMutationExit.
func (h *Host) handleFileMutationExit(conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var args FileMutationExitArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	ticket, ok := conn.fileMutations.take(args.Ticket)
	if !ok {
		return nil, fmt.Errorf("file mutation ticket %q is not held by this connection", args.Ticket)
	}
	ticket.Release()
	return &CallResultPayload{Result: json.RawMessage(`{}`)}, nil
}
