package rules

import (
	"regexp"
	"strings"
)

var (
	arrayRe = regexp.MustCompile(`^(?:readonly\s+)?(.+)\[\]$`)
	arrowRe = regexp.MustCompile(`^(?:new\s+)?\(`)
)

// SplitTop splits s on sep outside brackets, parentheses, braces, angle brackets and string literals.
func SplitTop(s, sep string) []string {
	var parts []string
	depth, start := 0, 0
	var quote rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			switch r {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case strings.ContainsRune("([{<", r):
			depth++
		case r == '>' && i > 0 && runes[i-1] == '=':
			// the arrow of a function type closes nothing
		case strings.ContainsRune(")]}>", r):
			depth--
		case depth == 0 && strings.HasPrefix(string(runes[i:]), sep):
			parts = append(parts, strings.TrimSpace(string(runes[start:i])))
			start = i + len([]rune(sep))
			i = start - 1
		}
	}
	return append(parts, strings.TrimSpace(string(runes[start:])))
}

// matchingParen reports whether the first parenthesis of s closes at its end.
func matchingParen(s string) bool {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(s)-1
			}
		}
	}
	return false
}

// IsFuncType reports whether an upstream type string is a function type.
func IsFuncType(up string) bool {
	up, _ = CutTypeParams(strings.TrimSpace(up))
	if !arrowRe.MatchString(up) {
		return false
	}
	for _, p := range SplitTop(up, "=>") {
		if p != up {
			return true
		}
	}
	return false
}

// IsSignalType reports whether an upstream type is an AbortSignal, possibly parenthesised and optional (`| undefined`, `| null`).
// A function type, object literal or array that only mentions AbortSignal is not a signal.
func IsSignalType(up string) bool {
	up = strings.TrimSpace(up)
	for strings.HasPrefix(up, "(") && strings.HasSuffix(up, ")") && matchingParen(up) {
		up = strings.TrimSpace(up[1 : len(up)-1])
	}
	signals := 0
	for _, m := range SplitTop(up, "|") {
		switch strings.TrimSpace(m) {
		case "undefined", "null":
		case "AbortSignal":
			signals++
		default:
			return false
		}
	}
	return signals == 1
}

// CutTypeParams splits a generic function type `<K extends A, V>(key: K) => V` into its type-parameter names and the rest `(key: K) => V`.
// A text that does not start with `<` is returned unchanged.
func CutTypeParams(up string) (string, []TypeParam) {
	if !strings.HasPrefix(up, "<") {
		return up, nil
	}
	depth := 0
	for i, r := range up {
		switch r {
		case '<':
			depth++
		case '>':
			if i > 0 && up[i-1] == '=' {
				continue
			}
			depth--
			if depth == 0 {
				var params []TypeParam
				for _, part := range SplitTop(up[1:i], ",") {
					name, rest, _ := strings.Cut(strings.TrimSpace(part), " ")
					if name == "" {
						continue
					}
					tp := TypeParam{Name: name}
					if bound, ok := strings.CutPrefix(strings.TrimSpace(rest), "extends "); ok {
						tp.Constraint, _, _ = strings.Cut(strings.TrimSpace(bound), " = ") // a default `= X` is no constraint
					}
					params = append(params, tp)
				}
				return strings.TrimSpace(up[i+1:]), params
			}
		}
	}
	return up, nil
}
