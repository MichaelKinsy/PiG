package regexpinfunc

import (
	"regexp"
	"sync"
)

func bad(s string) bool {
	return regexp.MustCompile(`^a+$`).MatchString(s) // want `regexp.MustCompile of a constant pattern inside a function compiles it on every call`
}

func badCompile(s string) bool {
	re, err := regexp.Compile("b") // want `regexp.Compile of a constant pattern`
	return err == nil && re.MatchString(s)
}

func badLiteral() func(string) bool {
	return func(s string) bool { return regexp.MustCompile("c").MatchString(s) } // want `regexp.MustCompile of a constant pattern`
}

// A package-level pattern compiles once.
var hoisted = regexp.MustCompile(`^a+$`)

func good(s string) bool { return hoisted.MatchString(s) }

// A pattern built at run time has to compile where it is known.
func dynamic(pattern, s string) bool {
	re, err := regexp.Compile(pattern)
	return err == nil && re.MatchString(s)
}

var onceValue = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("d") })

var (
	once   sync.Once
	lazyRe *regexp.Regexp
)

func onceDo() *regexp.Regexp {
	once.Do(func() { lazyRe = regexp.MustCompile("e") })
	return lazyRe
}

func init() { _ = regexp.MustCompile("f") }
