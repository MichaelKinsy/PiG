package codingagent

import "github.com/MichaelKinsy/PiG/internal/atomicfile"

// A file that cannot be replaced atomically (a file mounted into a container on its own, a hard-linked file) is written in place, as Pi writes it. That is
// not an error; the write is one line in the debug log, which PIG_DEBUG turns on, so that someone who debugs a storage problem can see which way it went.
func init() {
	atomicfile.SetInPlaceReporter(func(path string, cause error) {
		debugLog("%s was written in place instead of being replaced: %v", path, cause)
	})
}
