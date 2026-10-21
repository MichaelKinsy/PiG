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
	defer func() {
		args := map[string]string{"ticket": entered.Ticket}
		// The turn belongs to the process (the host's ticket registry is per process for a packed cell), not to the connection it was taken on: when that connection
		// is gone, because its extension was replaced or stopped while this task ran, another extension's connection of the process gives the turn back.
		if _, err := c.call(callFileMutationExit, args); err != nil {
			for _, other := range runningConnectionsExcept(c) {
				if _, err := other.call(callFileMutationExit, args); err == nil {
					break
				}
			}
		}
		// With no connection of the process left the host has released the turn already (it does when the last connection closes).
	}()
	return fn()
}
