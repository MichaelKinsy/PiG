package rules

import (
	"strings"
	"testing"
)

type keyofEnv struct {
	Env
	props map[string][]string
}

func (e keyofEnv) PropertyNames(name string) []string { return e.props[name] }

func TestKeyofRule(t *testing.T) {
	l := ouLoad(t, `
type Mode string
const (
	ModeA Mode = "a"
	ModeB Mode = "b"
)
type Partial string
const PartialA Partial = "a"
type Alias = string
const AliasA Alias = "a"
const AliasB Alias = "b"
type Num int
type Bag struct{}
`)
	env := keyofEnv{props: map[string][]string{"Modes": {"a", "b"}, "Empty": nil}}
	for _, tc := range []struct {
		name, up, typ string
		want          Tri
		why           string
	}{
		{"every property has a constant", "keyof Modes", "Mode", Yes, ""},
		{"a property without a constant", "keyof Modes", "Partial", No, "member b"},
		{"an alias of string accepts every value", "keyof Modes", "Alias", No, "not a named type"},
		{"a non-string named type", "keyof Modes", "Num", No, "not a string type"},
		{"a struct", "keyof Modes", "Bag", No, "string type"},
		{"an interface with no recorded properties is not decided", "keyof Empty", "Mode", Unknown, ""},
		{"an unknown interface is not decided", "keyof Nope", "Mode", Unknown, ""},
		{"not a keyof text", "Modes", "Mode", Unknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ty := l.typ(t, tc.typ)
			if tc.typ == "Alias" {
				ty = l.pkg.Scope().Lookup("Alias").Type()
			}
			v, ok := keyofRule{}.Type(env, tc.up, ty)
			if tc.want == Unknown {
				if ok {
					t.Fatalf("decided %+v, want undecided", v)
				}
				return
			}
			if !ok || v.OK != tc.want || !strings.Contains(v.Why, tc.why) {
				t.Fatalf("got %+v ok=%v, want %v containing %q", v, ok, tc.want, tc.why)
			}
		})
	}
}
