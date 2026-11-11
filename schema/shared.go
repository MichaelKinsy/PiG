package schema

// Ports packages/schema/src/shared.ts
//
// Regular expressions shared across all compiled schemas while one still uses them: tool schemas repeat a few patterns many times, and a regular expression is large once it has run. The cache holds its values weakly and drops an entry once its regular expression is collected, so it keeps nothing alive.

import (
	"fmt"
	"runtime"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/schema/internal/ecmaregex"
)

var sharedRegexes struct {
	sync.Mutex
	bySource map[string]weak.Pointer[ecmaregex.Regexp]
}

// sharedRegex returns the regular expression of a `pattern` source, shared by source. Patterns are ECMAScript regular expressions with the `u` flag; a source that is invalid with it (some older schemas escape characters `u` rejects) compiles without it. A source invalid either way fails the compile.
func sharedRegex(source string) *ecmaregex.Regexp {
	sharedRegexes.Lock()
	hit := sharedRegexes.bySource[source].Value()
	sharedRegexes.Unlock()
	if hit != nil {
		return hit
	}
	re, err := ecmaregex.Compile(source)
	if err != nil {
		re, err = ecmaregex.CompileAnnexB(source)
		if err != nil {
			fail("%v", err)
		}
	}
	sharedRegexes.Lock()
	defer sharedRegexes.Unlock()
	if hit := sharedRegexes.bySource[source].Value(); hit != nil {
		return hit
	}
	if sharedRegexes.bySource == nil {
		sharedRegexes.bySource = map[string]weak.Pointer[ecmaregex.Regexp]{}
	}
	sharedRegexes.bySource[source] = weak.Make(re)
	runtime.AddCleanup(re, dropRegex, source)
	return re
}

func dropRegex(source string) {
	sharedRegexes.Lock()
	defer sharedRegexes.Unlock()
	if entry, ok := sharedRegexes.bySource[source]; ok && entry.Value() == nil {
		delete(sharedRegexes.bySource, source)
	}
}

// sharedRegexCount is the number of cached regular expressions (for tests).
func sharedRegexCount() int {
	sharedRegexes.Lock()
	defer sharedRegexes.Unlock()
	return len(sharedRegexes.bySource)
}

// compileError is what a compile that fails panics with; Compile turns it into an error.
type compileError struct{ message string }

func fail(format string, args ...any) { panic(compileError{fmt.Sprintf(format, args...)}) }
