package main

// pi: packages/env/daemon/src/main.rs

// Ports the command line of packages/env/daemon/src/main.rs (main.rs:526-550): `--version` prints `pi-env <version>`,
// and a missing command, an unknown one or `serve` without `--token` print the usage line and exit 2.

import (
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func runCapturing(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	open := func(name string) *os.File {
		file, err := os.Create(dir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return file
	}
	in, out, errOut := open("in"), open("out"), open("err")
	code = run(args, in, out, errOut)
	read := func(name string) string {
		b, _ := os.ReadFile(dir + "/" + name)
		return string(b)
	}
	return code, read("out"), read("err")
}

func TestCommandLineMatchesPisDaemon(t *testing.T) {
	if code, out, _ := runCapturing(t, "--version"); code != 0 || out != "pi-env "+pigversion.UpstreamPackageVersion+"\n" {
		t.Fatalf("--version: %d %q", code, out)
	}
	for _, args := range [][]string{nil, {"bogus"}, {"serve"}, {"serve", "--token"}} {
		code, _, stderr := runCapturing(t, args...)
		if code != 2 || stderr != "usage: pi-env serve --token <hex>\n" {
			t.Errorf("%v: %d %q", args, code, stderr)
		}
	}
	if code, _, stderr := runCapturing(t, "serve", "--token", "tok"); code != 0 || strings.Contains(stderr, "usage") {
		t.Errorf("serve over an empty input: %d %q", code, stderr)
	}
}
