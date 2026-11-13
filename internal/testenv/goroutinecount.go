package testenv

import (
	"bytes"
	"runtime/pprof"
	"strings"
)

// LiveGoroutines counts the goroutines the goroutine profile lists. A test that compares the count before and after
// synctest.Wait uses it instead of runtime.NumGoroutine, which still counts, for a moment, a goroutine that has returned
// and that synctest.Wait already reported gone.
func LiveGoroutines() int {
	var stacks bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
	return strings.Count("\n\n"+stacks.String(), "\n\ngoroutine ")
}
