package main

import (
	"strings"
	"sync"
	"unicode"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// norm folds a name for comparison: lower case without separators, so ResolveHttpProxyUrl equals
// ResolveHTTPProxyURL and CODEMODE_OPTIONS_PREFIX equals CodemodeOptionsPrefix.
func norm(s string) string {
	if v, ok := normMemo.Load(s); ok {
		return v.(string)
	}
	n := normFold(s)
	normMemo.Store(s, n)
	return n
}

// normMemo caches norm: the detector folds the same few thousand names for every candidate of every row.
var normMemo sync.Map

func normFold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// Name rules, in order. A Go name stands for an upstream name when it is the upstream name (rule N1), the upstream name with an
// upper-cased first letter (N1), or equal after folding case and separators (N2: initialisms such as Http/HTTP, SCREAMING_SNAKE).
// A constructor is New plus the class name under the same two rules (N3). Anything else needs a documented rename (N4).
const (
	ruleExact  = "N1"
	ruleFolded = "N2"
	ruleCtor   = "N3"
	ruleRename = "N4"
)

// nameRule returns the rule under which goName stands for the upstream name, or "".
func nameRule(upstream, goName string) string {
	switch {
	case goName == upstream || goName == upperFirst(upstream):
		return ruleExact
	case norm(upstream) != "" && norm(goName) == norm(upstream):
		return ruleFolded
	}
	return rules.MatchName(upstream, goName)
}

// ctorRule matches New<Name> for a class constructor.
func ctorRule(class, goName string) string {
	rest, ok := strings.CutPrefix(goName, "New")
	if !ok || rest == "" {
		return ""
	}
	if nameRule(class, rest) != "" {
		return ruleCtor
	}
	return ""
}
