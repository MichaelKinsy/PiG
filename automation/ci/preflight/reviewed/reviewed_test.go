// SPDX-License-Identifier: MIT

package reviewed

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	good := "[[entry]]\npath = \"a/a_test.go\"\ncommit = \"0123456789abcdef\"\nclass = \"NOBEHAVIOR\"\ncite = \"\"\nreason = \"r\"\n"
	entries, err := Parse(good)
	if err != nil || len(entries) != 1 || entries[0].Path != "a/a_test.go" || entries[0].Class != "NOBEHAVIOR" {
		t.Fatalf("Parse(good) = %+v, %v", entries, err)
	}
	if entries, err := Parse(""); err != nil || len(entries) != 0 {
		t.Fatalf("an empty ledger holds no entries: %+v, %v", entries, err)
	}
	if _, err := Parse("[[entry]\npath = "); err == nil || !strings.Contains(err.Error(), File) {
		t.Fatalf("a malformed ledger must be an error naming %s: %v", File, err)
	}
}

func TestMatches(t *testing.T) {
	e := Entry{Path: "a/a_test.go", Commit: "0123456789"}
	full := "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name   string
		e      Entry
		commit string
		path   string
		want   bool
	}{
		{"prefix and path", e, full, "a/a_test.go", true},
		{"another path", e, full, "a/b_test.go", false},
		{"another commit", e, "ffffffffff" + full[10:], "a/a_test.go", false},
		{"prefix shorter than the minimum", Entry{Path: "a/a_test.go", Commit: "012345678"}, full, "a/a_test.go", false},
		{"no commit", Entry{Path: "a/a_test.go"}, full, "a/a_test.go", false},
	} {
		if got := tc.e.Matches(tc.commit, tc.path); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	piNames := map[string]bool{"agent.ts": true}
	recorded := func(id string) bool { return id == "D12" }
	for _, tc := range []struct {
		name string
		e    Entry
		want string
	}{
		{"CITE known", Entry{Class: "CITE", Cite: "packages/x/src/agent.ts:9", Reason: "r"}, ""},
		{"CITE unknown Pi file", Entry{Class: "CITE", Cite: "packages/x/src/invented.ts:9", Reason: "r"}, "does not name a Pi file"},
		{"CITE without a line", Entry{Class: "CITE", Cite: "packages/x/src/agent.ts", Reason: "r"}, "does not name a Pi file"},
		{"DIVERGENCE recorded", Entry{Class: "DIVERGENCE", Cite: "D12", Reason: "r"}, ""},
		{"DIVERGENCE without a record", Entry{Class: "DIVERGENCE", Cite: "D13", Reason: "r"}, "docs/parity/divergences/D13.md"},
		{"DIVERGENCE not an id", Entry{Class: "DIVERGENCE", Cite: "12", Reason: "r"}, "not a divergence id"},
		{"NOBEHAVIOR", Entry{Class: "NOBEHAVIOR", Reason: "r"}, ""},
		{"message-only", Entry{Class: "message-only", Reason: "r"}, ""},
		{"no reason", Entry{Class: "NOBEHAVIOR", Reason: " \t"}, "no reason"},
		{"unknown class", Entry{Class: "OTHER", Reason: "r"}, `unknown class "OTHER"`},
		{"empty class", Entry{Reason: "r"}, "unknown class"},
	} {
		err := tc.e.Validate(piNames, recorded)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one naming %q", tc.name, err, tc.want)
		}
	}
}
