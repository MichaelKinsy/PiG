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

// fileMutationTurns are the tickets the connections of one extension process hold, and have not exited. A ticket belongs to the process, not to the connection that
// was admitted: a packed Go, Rust or Python cell runs several extensions in one process, and an SDK helper that has no Context (the free function
// withFileMutationQueue) takes the turn on whichever of the process's connections is first running and gives it back on whichever is running then, so a member
// that is replaced or stops while another member's task holds a turn must not release it. The tickets are released when the last connection of the process has
// closed (the process exited, or every extension in it was replaced or shut down), so a crashed extension cannot keep a file blocked. An isolated extension,
// and a Node runtime (whose bridge asks over its own connection), has a registry of its own: its one connection is the process.
type fileMutationTurns struct {
	mu   sync.Mutex
	next uint64
	// live counts the connections that take their turns here and have not left; it is one for a registry of a connection's own.
	live int
	held map[string]*tools.MutationTicket
}

// join counts c among the connections that take their turns here.
func (t *fileMutationTurns) join() {
	t.mu.Lock()
	t.live++
	t.mu.Unlock()
}

// admit records a ticket the extension was admitted to and returns its id; false when c already left, in which case the caller releases the ticket.
func (t *fileMutationTurns) admit(c *Conn, ticket *tools.MutationTicket) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c.turnsLeft {
		return "", false
	}
	if t.held == nil {
		t.held = make(map[string]*tools.MutationTicket)
	}
	t.next++
	id := fmt.Sprintf("fm-%d", t.next)
	t.held[id] = ticket
	return id, true
}

// take removes and returns the ticket id names.
func (t *fileMutationTurns) take(id string) (*tools.MutationTicket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ticket, ok := t.held[id]
	delete(t.held, id)
	return ticket, ok
}

// leave ends c's part in the registry and refuses its later admissions. With the last connection gone it releases every held ticket.
func (t *fileMutationTurns) leave(c *Conn) {
	t.mu.Lock()
	c.turnsLeft = true
	t.live--
	if t.live > 0 {
		t.mu.Unlock()
		return
	}
	held := t.held
	t.held = nil
	t.mu.Unlock()
	for _, ticket := range held {
		ticket.Release()
	}
}

// turns is the registry c takes its turns in: its process's when the Host shared one (shareFileMutations), else c's own.
func (c *Conn) turns() *fileMutationTurns {
	if c.sharedTurns != nil {
		return c.sharedTurns
	}
	return &c.fileMutations
}

// shareFileMutations makes c take its turns in turns, the registry of the process c runs in. The Host calls it before the connection starts.
func (c *Conn) shareFileMutations(turns *fileMutationTurns) {
	turns.join()
	c.sharedTurns = turns
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
	turns := conn.turns()
	id, ok := turns.admit(conn, ticket)
	if !ok {
		ticket.Release()
		return nil, errors.New("extension connection closed")
	}
	result, err := json.Marshal(FileMutationEnterResult{Ticket: id})
	if err != nil {
		if released, held := turns.take(id); held {
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
	ticket, ok := conn.turns().take(args.Ticket)
	if !ok {
		return nil, fmt.Errorf("file mutation ticket %q is not held: it was released, or belongs to another extension process", args.Ticket)
	}
	ticket.Release()
	return &CallResultPayload{Result: json.RawMessage(`{}`)}, nil
}
