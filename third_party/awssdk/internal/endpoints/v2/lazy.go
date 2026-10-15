package endpoints

import (
	"regexp"
	"sync"
)

// LazyRegexp is a regular expression compiled on first use, so that the partition tables of every service do not
// compile their expressions at package initialization. Matching is that of regexp.Regexp.MatchString.
type LazyRegexp struct {
	expr string
	once sync.Once
	re   *regexp.Regexp
}

// NewLazyRegexp returns a LazyRegexp for expr. Like regexp.MustCompile, using it panics when expr does not compile.
func NewLazyRegexp(expr string) *LazyRegexp {
	return &LazyRegexp{expr: expr}
}

// MatchString reports whether s contains any match of the expression.
func (l *LazyRegexp) MatchString(s string) bool {
	l.once.Do(func() { l.re = regexp.MustCompile(l.expr) })
	return l.re.MatchString(s)
}
