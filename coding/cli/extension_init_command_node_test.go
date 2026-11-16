//go:build !pig_strip_node_extensions

package cli

import (
	"path/filepath"
	"testing"
)

func TestExtensionInitScaffoldsBuildAndRegister(t *testing.T) {
	if testing.Short() {
		t.Skip("scaffold registration builds language fixtures")
	}
	for _, language := range []string{"go", "python", "rust", "lisp", "node"} {
		if sdkLanguageStripped(language) {
			continue
		}
		for _, isolated := range []bool{false, true} {
			if isolated && language == "node" {
				continue // TestRunExtensionInitRejectsIsolatedNode
			}
			name := language + "-fixture"
			if isolated {
				name += "-isolated"
			}
			t.Run(name, func(t *testing.T) {
				requireSDKFixtureToolchain(t, language)
				t.Setenv("PIG_HOME", t.TempDir())
				root := filepath.Join(t.TempDir(), name)
				args := []string{root, "--lang", language, "--json"}
				if isolated {
					args = append(args, "--isolated")
				}
				if code := runExtensionInit(args); code != 0 {
					t.Fatalf("init code = %d", code)
				}
				reports, err := validateExtensionRuntimes(root)
				if err != nil {
					t.Fatal(err)
				}
				if len(reports) != 1 || !reports[0].Valid || !reports[0].Registered || reports[0].Name != name {
					t.Fatalf("validation reports = %#v", reports)
				}
				wantForm := "factory"
				if isolated {
					wantForm = "standalone"
				}
				if reports[0].Definition == nil || reports[0].Definition.Form != wantForm {
					t.Fatalf("definition = %#v, want %s", reports[0].Definition, wantForm)
				}
			})
		}
	}
}
