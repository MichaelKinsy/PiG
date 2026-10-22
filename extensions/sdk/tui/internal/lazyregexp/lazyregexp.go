// Package lazyregexp compiles a package-level regular expression when it is first used.
package lazyregexp

import (
	"regexp"
	"sync"
)

// Regexp is a lazily compiled *regexp.Regexp.
type Regexp struct {
	expr string
	once sync.Once
	re   *regexp.Regexp
}

// New returns a Regexp for expr. It does not compile expr.
func New(expr string) *Regexp { return &Regexp{expr: expr} }

func (r *Regexp) get() *regexp.Regexp {
	r.once.Do(func() { r.re = regexp.MustCompile(r.expr) })
	return r.re
}

// FindStringSubmatch is regexp.Regexp.FindStringSubmatch.
func (r *Regexp) FindStringSubmatch(s string) []string { return r.get().FindStringSubmatch(s) }

// MatchString is regexp.Regexp.MatchString.
func (r *Regexp) MatchString(s string) bool { return r.get().MatchString(s) }
