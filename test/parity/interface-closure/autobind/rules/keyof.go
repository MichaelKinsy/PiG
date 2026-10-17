package rules

import (
	"go/constant"
	"go/types"
	"regexp"
	"strings"
)

// U7 keyof T, for an upstream interface T, is a closed union of T's property names. The Go form is a named string type with a constant
// of that type whose value is each property name. A bare string, or an alias of string, accepts every value, so it refutes the union
// as U1 refutes a bare string for a literal union. Extra constants are not a refutation.
type keyofRule struct{}

var keyofRe = regexp.MustCompile(`^keyof\s+([A-Za-z_]\w*)$`)

func (keyofRule) Name() string { return "U7" }

func (keyofRule) Type(env Env, up string, t types.Type) (Verdict, bool) {
	m := keyofRe.FindStringSubmatch(strings.TrimSpace(up))
	if m == nil {
		return Verdict{}, false
	}
	props := env.PropertyNames(m[1])
	if len(props) == 0 {
		return Verdict{}, false
	}
	n, ok := t.(*types.Named)
	if !ok {
		return Refute("U7: keyof %s is a closed union of its property names, Go type %s is not a named type with constants", m[1], typeLabel(t)), true
	}
	if b, ok := n.Underlying().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
		return Refute("U7: keyof %s is a union of strings, Go type %s is not a string type", m[1], typeLabel(t)), true
	}
	have := map[string]bool{}
	for _, c := range ConstsOf(n) {
		if c.Val().Kind() == constant.String {
			have[constant.StringVal(c.Val())] = true
		}
	}
	var missing []string
	for _, p := range props {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return Refute("U7: no Go constant of %s for keyof %s member %s", n.Obj().Name(), m[1], strings.Join(missing, ", ")), true
	}
	return Accept("U7: %s has a constant for every property of %s", n.Obj().Name(), m[1]), true
}

func init() { RegisterType(Unions, keyofRule{}) }
