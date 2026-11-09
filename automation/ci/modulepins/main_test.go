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
