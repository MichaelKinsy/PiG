//go:build unix

package npmpublish_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/npmpublish"
	"github.com/MichaelKinsy/PiG/internal/npmpublish/npmtest"
)

// An npmCommand with a relative path names a file relative to the directory PiG runs in. A Package publishes in its own directory, so resolving the path there would run an npm the repository ships with the author's npm credentials.
func TestPublishResolvesARelativeNPMCommandInPiGsDirectory(t *testing.T) {
	fake := npmtest.Install(t)
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(npm)
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "tools", "npm"), program, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := writePackage(t, "1.0.0")
	marker := filepath.Join(t.TempDir(), "planted-npm-ran")
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "npm"), []byte("#!/bin/sh\n: > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	r := request(dir, npmpublish.Flags{}, &out)
	r.InPlace = true
	r.Command = []string{"./tools/npm"}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("publishing ran the npm in the package directory:\n%s", out.String())
	}
	want := [][]string{{"view", "@acme/tool@1.0.0", "version"}, {"publish", "--dry-run"}}
	var got [][]string
	for _, call := range fake.Calls() {
		got = append(got, call.Args)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("npm calls = %q, want %q\n%s", got, want, out.String())
	}
}
