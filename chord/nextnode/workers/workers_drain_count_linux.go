//go:build linux

package workers

import "golang.org/x/sys/unix"

// fionread is the request for the bytes a stream holds: Linux's FIONREAD, which x/sys names TIOCINQ (its value depends on
// the architecture).
const fionread = unix.TIOCINQ
