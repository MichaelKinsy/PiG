package main

import (
	"go/types"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// splitTop splits s on sep outside brackets, parentheses, braces, angle brackets and string literals.
func splitTop(s, sep string) []string { return rules.SplitTop(s, sep) }

var (
	stringLit   = regexp.MustCompile(`^"[^"]*"$|^'[^']*'$`)
	templateLit = regexp.MustCompile("^`[^`]*`$")
	numberLit   = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	genericRe   = regexp.MustCompile(`(?s)^([A-Za-z_][\w.]*)<(.*)>$`)
	identRe     = regexp.MustCompile(`^[A-Za-z_][\w.]*$`)
	arrayRe     = regexp.MustCompile(`(?s)^(?:readonly\s+)?(.+)\[\]$`)
	wrapperGen  = map[string]bool{"Partial": true, "Readonly": true, "Required": true, "NonNullable": true, "Omit": true, "Pick": true, "Awaited": true, "Awaitable": true, "MaybePromise": true}
)

// isFuncType reports whether an upstream type string is a function type.
func isFuncType(up string) bool { return rules.IsFuncType(up) }

// isSignalType reports whether an upstream type is an AbortSignal, possibly parenthesised and optional.
func isSignalType(up string) bool { return rules.IsSignalType(up) }

// stringLiteralUnion returns the literals of a union of string literals.
func stringLiteralUnion(up string) ([]string, bool) {
	var lits []string
	for _, m := range splitTop(up, "|") {
		m = strings.TrimSpace(m)
		if m == "undefined" || m == "null" {
			continue
		}
		if !stringLit.MatchString(m) {
			return nil, false
		}
		lits = append(lits, m[1:len(m)-1])
	}
	return lits, len(lits) > 0
}

func basicInfo(t types.Type) types.BasicInfo {
	if b, ok := t.Underlying().(*types.Basic); ok {
		return b.Info()
	}
	return 0
}

func isByteSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := s.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Byte
}

func namedOf(t types.Type) *types.Named {
	n, _ := types.Unalias(t).(*types.Named)
	return n
}

// deref strips pointers.
func deref(t types.Type) types.Type {
	for {
		p, ok := types.Unalias(t).(*types.Pointer)
		if !ok {
			return t
		}
		t = p.Elem()
	}
}

func isErrorType(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

func typeLabel(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

var mapLiteralRe = regexp.MustCompile(`(?s)^\{\s*\[\w+:\s*\w+\]:\s*(.+?);?\s*\}$`)

// mapLiteral returns the value type of an index-signature object type, or "".
func mapLiteral(up string) string {
	if m := mapLiteralRe.FindStringSubmatch(up); m != nil {
		return m[1]
	}
	return ""
}

func isContext(t types.Type) bool {
	n := namedOf(t)
	return n != nil && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}
