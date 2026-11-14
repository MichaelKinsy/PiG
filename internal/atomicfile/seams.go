package atomicfile

import (
	"os"
	"sync/atomic"
)

var (
	replaceTemp    = replace
	copyAttributes = func(*os.File, string) error { return nil }
)

var inPlaceReporter atomic.Pointer[func(path string, cause error)]

// SetInPlaceReporter sets the function that is told of a file written in place; nil removes it.
func SetInPlaceReporter(report func(path string, cause error)) {
	if report == nil {
		inPlaceReporter.Store(nil)
		return
	}
	inPlaceReporter.Store(&report)
}
