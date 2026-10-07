//go:build windows

package npmpublish_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/npmpublish"
	"github.com/MichaelKinsy/PiG/internal/npmpublish/npmtest"
)

// npmCmdShim writes dir\npm.cmd, which runs target with its arguments as Node's npm.cmd runs npm-cli.js, and returns its path. npm on Windows is such a shim, and cmd.exe runs it.
func npmCmdShim(t *testing.T, dir, target string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "npm.cmd")
	if err := os.WriteFile(shim, []byte("@\""+target+"\" %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return shim
}

func fakeNPM(t *testing.T) string {
	t.Helper()
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal(err)
	}
	return npm
}

func callArgs(calls []npmtest.Call) [][]string {
	args := make([][]string, 0, len(calls))
	for _, call := range calls {
		args = append(args, call.Args)
	}
	return args
}

// The npmCommand setting may name npm.cmd by a path with spaces, as Node's installer puts it in C:\Program Files\nodejs, and a Piglet's generated source is a folder argument. cmd.exe runs the shim, so the arguments must reach it escaped as cross-spawn escapes them; with Go's own quoting cmd.exe splits the shim's path at its first space once another argument is quoted.
func TestPublishRunsAnNPMShimAtAPathWithSpaces(t *testing.T) {
	fake := npmtest.Install(t)
	shim := npmCmdShim(t, filepath.Join(t.TempDir(), "Program Files", "nodejs"), fakeNPM(t))
	dir := filepath.Join(t.TempDir(), "Tom & Jerry", "piglet source")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageAt(t, dir, "1.0.0")
	var out bytes.Buffer
	for _, flags := range []npmpublish.Flags{{}, {Yes: true}} {
		r := request(dir, flags, &out)
		r.Command = []string{shim}
		if err := r.Run(context.Background()); err != nil {
			t.Fatalf("publish %+v: %v\n%s", flags, err, out.String())
		}
	}
	want := [][]string{{"view", "@acme/tool@1.0.0", "version"}, {"publish", dir, "--dry-run"}, {"view", "@acme/tool@1.0.0", "version"}, {"publish", dir}}
	if got := callArgs(fake.Calls()); !reflect.DeepEqual(got, want) {
		t.Fatalf("npm calls = %q, want %q\n%s", got, want, out.String())
	}
	if !fake.Published("@acme/tool", "1.0.0") {
		t.Fatalf("not published:\n%s", out.String())
	}
}

// cmd.exe looks for a bare command name in its working directory before PATH unless NoDefaultCurrentDirectoryInExePath is set, and a Package publishes in its own directory. npm must come from PATH, so that a repository's npm.cmd never runs with the author's npm credentials.
func TestPublishNeverRunsAnNPMFromThePackageDirectory(t *testing.T) {
	fake := npmtest.Install(t)
	bin := filepath.Join(t.TempDir(), "bin")
	npmCmdShim(t, bin, fakeNPM(t))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
	if err := os.Unsetenv("NoDefaultCurrentDirectoryInExePath"); err != nil {
		t.Fatal(err)
	}
	dir := writePackage(t, "1.0.0")
	marker := filepath.Join(t.TempDir(), "planted-npm-ran")
	if err := os.WriteFile(filepath.Join(dir, "npm.cmd"), []byte("@echo planted> \""+marker+"\"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := request(dir, npmpublish.Flags{}, &out)
	r.InPlace = true
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("publishing ran the npm.cmd in the package directory:\n%s", out.String())
	}
	want := [][]string{{"view", "@acme/tool@1.0.0", "version"}, {"publish", "--dry-run"}}
	if got := callArgs(fake.Calls()); !reflect.DeepEqual(got, want) {
		t.Fatalf("npm calls = %q, want %q\n%s", got, want, out.String())
	}
}

// An npmCommand naming a script makes cross-spawn look for the script's shebang interpreter in npm's working directory, the Package, before PATH, so on Windows npm must be a program Windows starts itself.
func TestPublishRefusesAnNPMScriptOnWindows(t *testing.T) {
	fake := npmtest.Install(t)
	script := filepath.Join(t.TempDir(), "npm-cli.js")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env fakenode\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writePackage(t, "1.0.0")
	planted, err := os.ReadFile(fakeNPM(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fakenode.exe"), planted, 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := request(dir, npmpublish.Flags{}, &out)
	r.InPlace = true
	r.Command = []string{script}
	err = r.Run(context.Background())
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("publishing ran the interpreter in the package directory: %q\n%s", callArgs(calls), out.String())
	}
	if err == nil || !strings.Contains(err.Error(), ".exe, .com, .cmd or .bat") {
		t.Fatalf("publish with an npm script = %v, want the refusal\n%s", err, out.String())
	}
}
