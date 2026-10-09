package main

import (
	"go/types"
	"testing"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

type testName struct{}

func (testName) Name() string { return "N-test" }
func (testName) Match(up, goName string) string {
	if up == "ident" && goName == "ID" {
		return "N-test"
	}
	return ""
}

type testType struct{}

func (testType) Name() string { return "T-test" }
func (testType) Type(_ rules.Env, up string, t types.Type) (rules.Verdict, bool) {
	if up == "Stamp" {
		if b, ok := t.Underlying().(*types.Basic); ok && b.Kind() == types.Int64 {
			return rules.Accept("T-test: Stamp is int64"), true
		}
		return rules.Refute("T-test: Stamp must be int64"), true
	}
	return rules.Verdict{}, false
}

type testSig struct{}

func (testSig) Name() string { return "S-test" }
func (testSig) Signature(_ rules.Env, up rules.CallShape, sig *types.Signature) (rules.Verdict, bool) {
	if up.Returns == "magic" && sig.Results().Len() == 1 {
		return rules.Accept("S-test"), true
	}
	return rules.Verdict{}, false
}

// TestRegisteredRulesAreConsultedAtTheirExtensionPoints: a family rule decides where the built-in rules do not, and never overrides a built-in yes.
func TestRegisteredRulesAreConsultedAtTheirExtensionPoints(t *testing.T) {
	defer rules.Isolate()()
	rules.RegisterName(rules.Naming, testName{})
	rules.RegisterType(rules.DataShapes, testType{})
	rules.RegisterSignature(rules.Functions, testSig{})
	rules.RegisterPlacement(rules.Placement, testPlace{})
	if nameRule("ident", "ID") == "" || nameRule("ident", "Other") != "" {
		t.Errorf("name rule: %q %q", nameRule("ident", "ID"), nameRule("ident", "Other"))
	}
	gt := goTypes(t, "var (\n\tI int64\n\tS string\n)\nfunc F() int { return 0 }\nfunc G() {}\n")
	c := &checker{generics: map[string]bool{}}
	if v := c.agree("Stamp", gt["I"]); v.ok != yes {
		t.Errorf("registered type rule should accept: %v %s", v.ok, v.why)
	}
	if v := c.agree("Stamp", gt["S"]); v.ok != no {
		t.Errorf("registered type rule should refute: %v %s", v.ok, v.why)
	}
	if v := c.agree("string", gt["S"]); v.ok != yes {
		t.Errorf("a built-in yes stays yes: %v", v.ok)
	}
	if v := c.signature(callShape{Returns: "magic"}, gt["F"].(*types.Signature)); v.ok != yes {
		t.Errorf("registered signature rule should accept: %v %s", v.ok, v.why)
	}
	if v := c.signature(callShape{Returns: "magic"}, gt["G"].(*types.Signature)); v.ok == yes {
		t.Errorf("a rule that does not apply must leave the built-in verdict")
	}
	if dirs := rules.Dirs("zzz"); len(dirs) != 1 || dirs[0] != "extra/dir" {
		t.Errorf("placement dirs: %v", dirs)
	}
}

type testPlace struct{}

func (testPlace) Name() string { return "P-test" }
func (testPlace) Dirs(pkg string) []string {
	if pkg == "zzz" {
		return []string{"extra/dir"}
	}
	return nil
}
