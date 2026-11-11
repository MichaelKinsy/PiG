// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeModule(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel, "go.mod")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFlagsAReplacingModuleOffTheRootSDKPin(t *testing.T) {
	const pin = "v0.7.1-0.20261108053302-4e194449dcd7"
	root := t.TempDir()
	writeModule(t, root, ".", "module github.com/MichaelKinsy/PiG\n\ngo 1.27.0\n\nrequire "+sdkPath+" "+pin+"\n")
	writeModule(t, root, "fix/stale", "module example.com/stale\n\ngo 1.27.0\n\nrequire (\n\t"+rootPath+" v0.7.0\n\t"+sdkPath+" v0.7.0\n)\n\nreplace "+sdkPath+" => ../../x\n")
	writeModule(t, root, "fix/current", "module example.com/current\n\ngo 1.27.0\n\nrequire "+sdkPath+" "+pin+"\n\nreplace "+sdkPath+" => ../../x\n")
	writeModule(t, root, "fix/published", "module example.com/published\n\ngo 1.27.0\n\nrequire "+sdkPath+" v0.7.0\n")
	problems, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want the stale module's two requirements only", problems)
	}
	for _, problem := range problems {
		if !strings.HasPrefix(problem, filepath.Join("fix", "stale", "go.mod")+" ") {
			t.Errorf("problem %q does not name the stale module", problem)
		}
	}
}

// The repository itself: a root SDK bump that leaves a replacing in-tree module on the old version fails here, and in ci-bookkeeping through make module-pins, rather than as "updates to go.mod needed" in every Go strict and packed conformance cell.
func TestRepositoryModulesPinTheRootSDKVersion(t *testing.T) {
	problems, err := check(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

func TestCheckTemplatesFlagsAGoModWrittenWithAnotherVersion(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("verb_test.go", "package x\n\nvar a = \"require github.com/MichaelKinsy/PiG/extensions/sdk v%s\\n\"\n")
	write("literal_test.go", "package x\n\nvar b = \"require github.com/MichaelKinsy/PiG v0.7.0\\n\"\n")
	write("release_test.go", "package x\n\nvar c = fmt.Sprintf(\"module m\\nrequire (\\n github.com/MichaelKinsy/PiG %s\\n)\\n\",\n\tpigversion.PigVersion)\n")
	write("placeholder_test.go", "package x\n\nvar d = \"require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\\n\"\n")
	write("helper_test.go", "package x\n\nvar e = fmt.Sprintf(\"require github.com/MichaelKinsy/PiG/extensions/sdk %s\\n\", version)\n")
	write("unrelated_test.go", "package x\n\nvar f = pigversion.PigVersion\n")
	problems, err := checkTemplates(root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(problems, "\n")
	for _, flagged := range []string{"verb_test.go:3", "release_test.go:4"} {
		if !strings.Contains(got, flagged) {
			t.Errorf("problems %q do not flag %s", got, flagged)
		}
	}
	for _, clean := range []string{"literal_test.go", "placeholder_test.go", "helper_test.go", "unrelated_test.go"} {
		if strings.Contains(got, clean) {
			t.Errorf("problems %q flag %s", got, clean)
		}
	}
}
