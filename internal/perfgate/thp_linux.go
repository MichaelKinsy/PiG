// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import "golang.org/x/sys/unix"

// DisableTransparentHugePages stops the kernel from backing this process's anonymous memory with transparent huge pages. The setting is inherited across fork and kept across exec, so every process the caller starts afterwards (a measured pig, the tmux server a pane's pig runs under) is measured with 4 KiB pages.
//
// The memory ceilings were derived on a host whose THP policy is "madvise", where the Go runtime's heap is not huge-page backed. GitHub's Ubuntu runners use "always": a heap that touches 3 MB of one 8 MB arena then shows as two or three whole 2 MiB pages (AnonHugePages 4 to 6 MB), so the same binary measured an anonymous 5.65 to 7.77 MB there instead of 3.9 to 4.0 MB. Which of those it showed depended on where the heap's pages fell relative to the 2 MiB boundaries, not on what the code allocated, so the figure could neither gate code changes nor be compared across hosts.
func DisableTransparentHugePages() error {
	return unix.Prctl(unix.PR_SET_THP_DISABLE, 1, 0, 0, 0)
}
