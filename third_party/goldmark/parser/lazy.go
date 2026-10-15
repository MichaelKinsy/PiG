package parser

import (
	"regexp"
	"sync"
)

// lazyRegexp returns a function that compiles expr on its first call, so importing the package does not compile every block and inline pattern.
func lazyRegexp(expr string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(expr) })
}
