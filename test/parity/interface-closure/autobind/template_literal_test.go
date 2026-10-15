package main

import "testing"

// T1t: a template literal type (Pi keys.ts:236 `ctrl: <K extends BaseKey>(key: K): `ctrl+${K}“) is a string, so it agrees with a Go string type and
// not with a number.
func TestTemplateLiteralTypeIsAString(t *testing.T) {
	gt := goTypes(t, "type KeyID string\nvar (\n\tK KeyID\n\tN int\n)")
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "tui"}
	if got := c.agree("`ctrl+${K}`", gt["K"]); got.ok != yes {
		t.Errorf("template literal vs KeyID = %v (%s), want yes", got.ok, got.why)
	}
	if got := c.agree("`ctrl+${K}`", gt["N"]); got.ok == yes {
		t.Errorf("template literal accepted an int")
	}
}
