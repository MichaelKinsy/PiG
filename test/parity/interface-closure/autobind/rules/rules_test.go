package rules

import (
	"go/types"
	"strings"
	"testing"
)

type fakeType struct{ name string }

func (r fakeType) Name() string { return r.name }
func (r fakeType) Type(_ Env, up string, _ types.Type) (Verdict, bool) {
	switch up {
	case "yes":
		return Accept("T-test: %s", up), true
	case "no":
		return Refute("T-test: %s", up), true
	case "unknown":
		return Undecided("T-test"), true
	}
	return Verdict{}, false
}

func TestRegisteredTypeRuleDecidesOnlyWhenSure(t *testing.T) {
	t.Cleanup(Isolate()) // every family's registrations, not only the type rules, are in Registered
	RegisterType(Unions, fakeType{"b-rule"})
	RegisterType(Naming, fakeType{"a-rule"})
	if got := Registered(); strings.Join(got, ",") != "naming/a-rule,unions/b-rule" {
		t.Fatalf("registration order: %v", got)
	}
	for up, want := range map[string]Tri{"yes": Yes, "no": No} {
		if v, ok := CheckType(nil, up, nil); !ok || v.OK != want {
			t.Errorf("%s: %+v %v", up, v, ok)
		}
	}
	for _, up := range []string{"unknown", "other"} {
		if _, ok := CheckType(nil, up, nil); ok {
			t.Errorf("%s must not decide", up)
		}
	}
}

func TestDuplicateAndUnknownFamilyPanic(t *testing.T) {
	saved := typeRules
	t.Cleanup(func() { typeRules = saved })
	typeRules = nil
	RegisterType(Naming, fakeType{"x"})
	for name, f := range map[string]func(){
		"duplicate": func() { RegisterType(Unions, fakeType{"x"}) },
		"family":    func() { RegisterType(Family(99), fakeType{"y"}) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s registration must panic", name)
				}
			}()
			f()
		}()
	}
}
