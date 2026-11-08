package sdk

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// Host calls for Pi's withFileMutationQueue (core/tools/file-mutation-queue.ts). Pi has one queue in one process, shared by the built-in edit and write tools and
// every extension. An extension here runs in a process of its own, so the queue lives in the host and the SDK asks it for the file's turn (fileMutation.enter,
// answered once the file's earlier mutations completed) and gives the turn back (fileMutation.exit). The SDK keeps no queue and no path-key code of its own.
const (
	callFileMutationEnter = "fileMutation.enter"
	callFileMutationExit  = "fileMutation.exit"
)

// WithFileMutationQueue runs fn once every mutation registered earlier for the same file has finished, then lets the next one run, and returns fn's result and error.
// Mutations of different files run in parallel. It mirrors Pi's withFileMutationQueue, which edit and write extension tools wrap around their read-modify-write so
// parallel tool calls on one file cannot interleave: the file is keyed by its real path (a symlink and its target share a queue; a path that does not exist yet is
// keyed by its absolute path), and callers run in the order they called. A path that cannot be resolved for a reason other than a missing file or directory
// returns that error without running fn.
//
// The queue is the host's: it serializes this extension's mutations with the host's built-in edit and write tools and with every other extension process, as Pi's
// one queue does. The turn is held by the extension's process: a packed cell's members share it, so one member stopping while another's fn runs does not release
// the file, and the host releases it when the process's last connection closes. Like the other host helpers it may be called before Run, and with no connection to the host (PIG_EXT_SOCKET unset) it returns an error without running fn.
func WithFileMutationQueue[T any](filePath string, fn func() (T, error)) (T, error) {
	var result T
	// Pi resolves the path against the process's working directory; the host resolves nothing relative, so the SDK sends it absolute.
	resolved, err := filepath.Abs(filePath)
	if err != nil {
		return result, err
	}
	if hook := installedHelpers(); hook != nil && hook.WithFileMutationQueue != nil {
		err := hook.WithFileMutationQueue(resolved, func() error {
			var taskErr error
			result, taskErr = fn()
			return taskErr
		})
		return result, err
	}
	c, err := helperConnection()
	if err != nil {
		return result, err
	}
	reply, err := c.call(callFileMutationEnter, map[string]string{"path": resolved})
	if err := callResultError(reply, err); err != nil {
		return result, err
	}
	var entered struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(reply.Result, &entered); err != nil {
		return result, fmt.Errorf("decode %s: %w", callFileMutationEnter, err)
	}
	if entered.Ticket == "" {
		return result, fmt.Errorf("%s returned no ticket", callFileMutationEnter)
	}
	defer releaseFileMutationTurn(c, entered.Ticket)
	return fn()
}

// releaseFileMutationTurn gives the turn back. The turn belongs to the process (the host's ticket registry is per process for a packed cell), not to the connection it was taken on: when that connection
// is gone, because its extension was replaced or stopped while the task ran, another extension's connection of the process gives the turn back.
func releaseFileMutationTurn(c *conn, ticket string) {
	args := map[string]string{"ticket": ticket}
	if _, err := c.call(callFileMutationExit, args); err != nil {
		for _, other := range runningConnectionsExcept(c) {
			if _, err := other.call(callFileMutationExit, args); err == nil {
				break
			}
		}
	}
	// With no connection of the process left the host has released the turn already (it does when the last connection closes).
}

// fileMutationEnterArgs is the argument of fileMutation.enter (coding/extension/host/subprocess FileMutationEnterArgs).
type fileMutationEnterArgs struct {
	Path string `json:"path"`
	// Request is the tool_call request the enter serves; empty for the free function, and for a Context that serves no tool call.
	Request string `json:"request,omitempty"`
}

// WithFileMutationQueue is [WithFileMutationQueue] for a handler that has its Context: it runs fn in the host's file mutation queue for filePath, in the place Pi gives a
// tool call of a parallel batch, and returns fn's error. A tool that takes the queue through its Context registers in the order Pi's single thread runs the batch's
// tools: Pi starts every call of the batch in source order and each runs to its first await, and withFileMutationQueue registers in the order it is called
// (core/tools/file-mutation-queue.ts: registrationQueue), so two tools that reach the queue after the same number of awaits register in call order, and one that awaited
// real work first registers after the one that did not. A native handler runs on a thread of its own, so the free function registers in the order the calls reach the
// host, which is the order the threads got there. The Context form names its tool call, and the host registers it only after the start windows of the extension tools
// before it in the batch closed: a tool's window is open until its first host call that returns a Promise in Pi (an exec, a dialog, a model call, this one), its answer or
// its end, so a tool that makes only getter calls before it takes the queue still registers before the tool behind it, however many round trips its getters took. The
// price is the one docs/extension-api-parity.md names for the edit and write calls behind a native tool: a tool that does work of its own, with no host call, before it
// takes the queue holds the queue registrations of the tools behind it (D187).
//
// A Context that serves no tool call (a command, an event, a retained Context) registers as the free function does. Like the free function it waits for the file with no
// regard for the request's cancellation, as Pi's queue does.
func (c Context) WithFileMutationQueue(filePath string, fn func() error) error {
	resolved, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}
	if c.hostConnection() == nil {
		return errBusNotConnected
	}
	request := c.requestID
	if c.parent != nil {
		request = c.parent.id
	}
	if c.toolCallID == "" {
		request = ""
	}
	reply, err := c.callHostAfterBegin(callFileMutationEnter, fileMutationEnterArgs{Path: resolved, Request: request}, true, nil)
	if err := callResultError(reply, err); err != nil {
		return err
	}
	var entered struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(reply.Result, &entered); err != nil {
		return fmt.Errorf("decode %s: %w", callFileMutationEnter, err)
	}
	if entered.Ticket == "" {
		return fmt.Errorf("%s returned no ticket", callFileMutationEnter)
	}
	defer releaseFileMutationTurn(c.hostConnection(), entered.Ticket)
	return fn()
}
