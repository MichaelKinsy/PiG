//go:build !unix && !windows

package childwait

import "os"

const pollSupported = false

// pollReadable has no answer where poll(2) is not available for a pipe: the reader goes straight to its read.
func pollReadable(*os.File, int) (readable, closed bool) { return true, false }

// pollPending has no answer where poll(2) is not available for a pipe: no reader waits for another.
func pollPending(*os.File) bool { return false }
