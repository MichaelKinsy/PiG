// SPDX-License-Identifier: MIT

//go:build !linux

package perfgate

// DisableTransparentHugePages does nothing: the memory gates read smaps_rollup, which only Linux has.
func DisableTransparentHugePages() error { return nil }
