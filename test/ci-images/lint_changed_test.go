// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Go's recursive package patterns ignore testdata directories. In particular,
// their nested fixture modules must not enter the root module's lint invocation.
func TestLintChangedPackageSelection(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	requireBashMake(t)
	for _, tc := range []struct {
		name       string
		files      []string
		packages   []string
		lintStatus string
	}{
		{name: "no Go changes", files: []string{"README.md"}},
		{name: "fixtures only", files: []string{
			"testdata/root/main.go",
			"test/extension-conformance/testdata/provider-go-sibling/extension.go",
			"test/parity/testdata/ai-sdk-pig/main.go",
		}},
		{name: "mixed changes", files: []string{
			"embed.go",
			"ai/provider.go",
			"ai/provider_test.go",
			"test/extension-conformance/testdata/provider-go-sibling/extension.go",
			"test/extension-conformance/conformance_test.go",
			"test/parity/runner/testdata_auth.go",
			"testdata-helper/main.go",
			"extensions/sdk/extension.go",
			"wasmonly/main.go",
		}, packages: []string{"./.", "./ai", "./extensions/sdk", "./test/extension-conformance", "./test/parity/runner", "./testdata-helper"}},
		{name: "lint findings fail", files: []string{"ai/provider.go"}, packages: []string{"./ai"}, lintStatus: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, path := range []string{"Makefile", "automation/make/parity.mk", "automation/make/ci.mk", "internal/coding/pigversion/pigversion.go", "coding/upstream.go"} {
				copyCIFixture(t, root, path)
			}
			for _, path := range tc.files {
				writeCIFixture(t, root, path, "")
			}
			writeCIFixture(t, root, "test/extension-conformance/testdata/provider-go-sibling/go.mod", "module fixture\n")
			writeCIFixture(t, root, "bin/git", `#!/bin/sh
case "$*" in
  'merge-base HEAD fixture') printf 'base-commit\n' ;;
  'diff --name-only base-commit...HEAD'|'diff --name-only'|'diff --name-only --cached'|'ls-files --others --exclude-standard') printf '%s\n' "$CHANGED_FILES" ;;
  *) exit 99 ;;
esac
`)
			writeCIFixture(t, root, "bin/go", `#!/bin/sh
if [ "$1" = list ]; then
  # A package with no Go files for the host platform (a wasm-only package) lists no files.
  case "$*" in *wasmonly*) printf '[][][]\n' ;; *) printf '[main.go][][]\n' ;; esac
  exit 0
fi
printf '%s\n' "$@" > "$LINT_LOG"
exit "${LINT_STATUS:-0}"
`)
			log := filepath.Join(root, "lint-args")
			t.Setenv("CHANGED_FILES", strings.Join(tc.files, "\n"))
			t.Setenv("LINT_LOG", log)
			t.Setenv("LINT_STATUS", tc.lintStatus)
			t.Setenv("PIG_DEV_HOME", root)
			cmd := mockedBash(t, filepath.Join(root, "bin"), "-c", "exec make lint-changed LINT_BASE=fixture")
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if (err != nil) != (tc.lintStatus != "") {
				t.Fatalf("lint-changed: %v\n%s", err, output)
			}
			if len(tc.packages) == 0 {
				if _, err := os.Stat(log); !os.IsNotExist(err) {
					t.Fatalf("linter must not run without changed packages: %v\n%s", err, output)
				}
				return
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string{"tool", "golangci-lint", "run", "--allow-parallel-runners", "--build-tags=integration,live,parity", "--new-from-rev=base-commit", "--whole-files"}, tc.packages...)
			if got := strings.Fields(string(data)); !slices.Equal(got, want) {
				t.Fatalf("lint arguments:\ngot  %q\nwant %q", got, want)
			}
		})
	}
}
