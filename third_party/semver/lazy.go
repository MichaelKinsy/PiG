package semver

import (
	"regexp"
	"sync"
)

// lazyRegexp is a regular expression compiled on first use. PiG fork: upstream compiles them in init, which costs every process that links the package.
type lazyRegex struct {
	expr string
	once sync.Once
	re   *regexp.Regexp
}

func lazyRegexp(expr string) *lazyRegex { return &lazyRegex{expr: expr} }

func (l *lazyRegex) get() *regexp.Regexp {
	l.once.Do(func() { l.re = regexp.MustCompile(l.expr) })
	return l.re
}

func (l *lazyRegex) MatchString(s string) bool { return l.get().MatchString(s) }

func (l *lazyRegex) FindAllString(s string, n int) []string { return l.get().FindAllString(s, n) }

func (l *lazyRegex) FindStringSubmatch(s string) []string { return l.get().FindStringSubmatch(s) }

func (l *lazyRegex) FindAllStringSubmatch(s string, n int) [][]string {
	return l.get().FindAllStringSubmatch(s, n)
}
