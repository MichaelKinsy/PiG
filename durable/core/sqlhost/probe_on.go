// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package sqlhost

import "context"

// QueuedPosts is the number of items waiting in the owner mailbox. A test that holds the owner in a commit (Options.OnCommit) waits on
// it to know a caller's request has reached the owner's queue, in the order Pi's synchronous enqueue gives. It exists only in the
// pigfault test build.
func (h *Host) QueuedPosts() int {
	h.box.mu.Lock()
	defer h.box.mu.Unlock()
	return len(h.box.items) - h.box.head
}

// Flush returns once the owner has nothing queued and no hops chain open: everything posted before the call ran, with the
// microtask chains it started. A test that leaves the owner free waits on it as Pi's does on `await flush()`. It exists only in the
// pigfault test build.
func (h *Host) Flush(ctx context.Context) error {
	done := make(chan struct{})
	var again func(error)
	again = func(dead error) {
		h.box.mu.Lock()
		queued := len(h.box.items) - h.box.head
		h.box.mu.Unlock()
		if dead == nil && (queued > 0 || h.chains > 0) {
			h.box.put(again)
			return
		}
		close(done)
	}
	if !h.enqueue(again) {
		return h.closedErr()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
