// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package sqlhost

// QueuedPosts is the number of items waiting in the owner mailbox. A test that holds the owner in a commit (Options.OnCommit) waits on
// it to know a caller's request has reached the owner's queue, in the order Pi's synchronous enqueue gives. It exists only in the
// pigfault test build.
func (h *Host) QueuedPosts() int {
	h.box.mu.Lock()
	defer h.box.mu.Unlock()
	return len(h.box.items)
}
