package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// pi: packages/chord/src/next/contracts/names.ts
//
// Mirrors the restricted-name cases of packages/chord/test/documents/definition.test.ts ("takes a restricted name
// only: lowercase [a-z0-9@/._~-]"), which reach checkName through defineState.

func TestCheckNameTakesRestrictedNamesOnly(t *testing.T) {
	for _, name := range []string{"@app/notes.v_1~x-y", "n", "0-9"} {
		if err := contracts.CheckName(name, "A state name"); err != nil {
			t.Errorf("CheckName(%q) = %v, want nil", name, err)
		}
	}
	// name and JSON.stringify(name); "a\uD800" is the lone surrogate as the WTF-8 bytes of U+D800.
	for _, c := range []struct{ name, quoted string }{
		{"", `""`},
		{"Notes", `"Notes"`},
		{"app:notes", `"app:notes"`},
		{"app notes", `"app notes"`},
		{"a\x00b", `"a\u0000b"`},
		{"a\xed\xa0\x80", `"a\ud800"`},
		{"é", `"é"`},
		{"a{", `"a{"`},
		{"a`", "\"a`\""},
		{"a[", `"a["`},
		{"a-Z", `"a-Z"`},
	} {
		err := contracts.CheckName(c.name, "A state name")
		want := "A state name " + c.quoted + ": expected lowercase [a-z0-9@/._~-]"
		if err == nil || err.Error() != want {
			t.Errorf("CheckName(%q) = %v, want %q", c.name, err, want)
		}
	}
}
