// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// REUSE-IgnoreStart

// A license tag declares a license where it stands in a comment (or alone on its line, as in a .license file). The
// same text inside a string literal or a value is data of the program.
func TestLicenseTagsCountOnlyInComments(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		ids  []string
	}{
		{"Go line comment", "// SPDX-License-Identifier: MIT", []string{"MIT"}},
		{"indented line comment", "\t// SPDX-License-Identifier: MIT", []string{"MIT"}},
		{"Rust doc comments", "//! SPDX-License-Identifier: Apache-2.0", []string{"Apache-2.0"}},
		{"C block comment", "/* SPDX-License-Identifier: GPL-2.0-only */", []string{"GPL-2.0-only"}},
		{"block comment continuation", " * SPDX-License-Identifier: Apache-2.0 OR MIT", []string{"Apache-2.0", "MIT"}},
		{"shell, Python, PowerShell or YAML comment", "# SPDX-License-Identifier: MIT", []string{"MIT"}},
		{"indented hash comment", "    # SPDX-License-Identifier: BSD-3-Clause", []string{"BSD-3-Clause"}},
		{"PowerShell block comment", "<# SPDX-License-Identifier: MIT #>", []string{"MIT"}},
		{"Lisp comment", ";;; SPDX-License-Identifier: MIT", []string{"MIT"}},
		{"HTML or Markdown comment", "<!-- SPDX-License-Identifier: CC-BY-4.0 -->", []string{"CC-BY-4.0"}},
		{"SQL or Lua comment", "-- SPDX-License-Identifier: MIT", []string{"MIT"}},
		{"reStructuredText comment", ".. SPDX-License-Identifier: MIT", []string{"MIT"}},
		{"batch comment", "REM SPDX-License-Identifier: MIT", []string{"MIT"}},
		{"a .license file", "SPDX-License-Identifier: GPL-3.0-only WITH GCC-exception-3.1", []string{"GPL-3.0-only", "GCC-exception-3.1"}},

		{"Go string literal", `	want := "// SPDX-License-Identifier: GPL-3.0-only"`, nil},
		{"Go string literal of a patch", `	d.Files = append(d.Files, prFile{Patch: "@@ -0,0 +1 @@\n+// SPDX-License-Identifier: GPL-3.0-only"})`, nil},
		{"Go raw string on one line", "\theader := `# SPDX-License-Identifier: GPL-3.0-only`", nil},
		{"shell double-quoted string", `echo "# SPDX-License-Identifier: GPL-3.0-only" > "$out"`, nil},
		{"shell single-quoted string", `printf '%s\n' 'SPDX-License-Identifier: GPL-3.0-only'`, nil},
		{"Python string", `HEADER = "SPDX-License-Identifier: GPL-3.0-only"`, nil},
		{"YAML quoted value", `    text: 'SPDX-License-Identifier: GPL-3.0-only'`, nil},
		{"YAML plain value", `    pattern: SPDX-License-Identifier: GPL-3.0-only`, nil},
		{"YAML value holding a comment", `    header: "# SPDX-License-Identifier: GPL-3.0-only"`, nil},
		{"prose", "Every file starts with SPDX-License-Identifier: MIT.", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, header := declaredLicenses(tc.line)
			if !slices.Equal(ids, tc.ids) || header != (tc.ids != nil) {
				t.Fatalf("%q declares %q (header %v), want %q", tc.line, ids, header, tc.ids)
			}
		})
	}
}

// A test of this rule holds foreign tags in its string literals; adding it declares nothing.
func TestLicenseTagInAStringLiteralIsNotADeclaration(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "LICENSES", "MIT.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := params{"notice-files": []any{"THIRD_PARTY_NOTICES.md"}, "code-files": `\.go$`}
	patch := "@@ -0,0 +1,3 @@\n+// SPDX-License-Identifier: MIT\n+package a\n+" + `var patch = "@@ -0,0 +1 @@\n+// SPDX-License-Identifier: GPL-3.0-only"`
	d := &data{Files: []prFile{{Filename: "a/rules_test.go", Status: "added", Patch: patch}}}
	if o := ruleLicenseNotices(d, p, &ruleEnv{root: root}); o.result != resultPass || len(o.warnings) != 0 {
		t.Fatalf("outcome %+v, want a pass without warnings", o)
	}
	d.Files[0].Patch += "\n+// SPDX-License-Identifier: GPL-3.0-only"
	if o := ruleLicenseNotices(d, p, &ruleEnv{root: root}); o.result != resultFail || !strings.Contains(o.reason, "a/rules_test.go:4 declares GPL-3.0-only") {
		t.Fatalf("outcome %+v, want a fail for the comment on line 4", o)
	}
}

// REUSE-IgnoreEnd
