// SPDX-License-Identifier: MIT

package reviewed

import (
	"strings"
	"testing"
)

func TestParseToolingAndInTooling(t *testing.T) {
	list, err := ParseTooling("expectations_from = \"2026-10-10T00:00:00Z\"\n\n[[tooling]]\nprefix = \"automation/ci/preflight/\"\nreason = \"r\"\n\n[[golden]]\npaths = [\"x\"]\n\n[[tooling]]\nprefix = \"automation/ci/other/\"\nreason = \"s\"\n")
	if err != nil || len(list) != 2 || list[0].Prefix != "automation/ci/preflight/" || list[1].Reason != "s" {
		t.Fatalf("ParseTooling = %+v, %v", list, err)
	}
	for _, tc := range []struct {
		file   string
		prefix string
		ok     bool
	}{
		{"automation/ci/preflight/a_test.go", "automation/ci/preflight/", true},
		{"automation/ci/other/deep/a_test.go", "automation/ci/other/", true},
		{"automation/ci/preflightx/a_test.go", "", false},
		{"agent/a_test.go", "", false},
	} {
		if prefix, ok := InTooling(list, tc.file); prefix != tc.prefix || ok != tc.ok {
			t.Errorf("InTooling(%q) = %q, %v, want %q, %v", tc.file, prefix, ok, tc.prefix, tc.ok)
		}
	}
	if _, ok := InTooling([]Tooling{{Prefix: ""}}, "a_test.go"); ok {
		t.Error("an empty prefix holds every file")
	}
	if list, err := ParseTooling(""); err != nil || len(list) != 0 {
		t.Errorf("an empty file is no list and no error: %+v, %v", list, err)
	}
	if _, err := ParseTooling("[[tooling]\nprefix = "); err == nil || !strings.Contains(err.Error(), ProvenanceFile) {
		t.Errorf("a file that is not TOML: %v", err)
	}
}
