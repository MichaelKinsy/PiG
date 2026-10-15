package util

import (
	"regexp"
	"sync"
)

// lazyRegexp returns a function that compiles expr on its first call.
func lazyRegexp(expr string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(expr) })
}
