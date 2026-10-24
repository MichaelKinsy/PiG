//go:build !unix && !windows

package childwait

import "os"

// pollPipe has no answer where the platform has neither poll(2) nor a pipe query: the grace decides, as upstream's idle timer does.
func pollPipe(*os.File) (hungUp, unread, known bool) { return false, false, false }
