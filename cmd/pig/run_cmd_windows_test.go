//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// buildArgsEcho builds a program that prints its arguments as JSON, and a
// .cmd shim in a directory containing spaces that forwards %* to it -- the
// shape of npm.cmd forwarding to node.exe.
func buildArgsEcho(t *testing.T) (shim string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module argsecho\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nimport (\n\t\"encoding/json\"\n\t\"os\"\n)\n\nfunc main() { _ = json.NewEncoder(os.Stdout).Encode(os.Args[1:]) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tool dir with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "argsecho.exe"), ".")
	build.Dir = src
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build argsecho: %v\n%s", err, output)
	}
	shim = filepath.Join(dir, "tool.cmd")
	if err := os.WriteFile(shim, []byte("@\"%~dp0argsecho.exe\" %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return shim
}

// npmShimTemplate is the .cmd shim npm writes into node_modules\.bin (as in
// extensions\sdk-ts\node_modules\.bin\pi.cmd); %s is the script path relative
// to the shim. cmd.exe parses its arguments a second time.
const npmShimTemplate = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\nIF EXIST \"%%dp0%%\\node.exe\" (\r\n  SET \"_prog=%%dp0%%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%%PATHEXT:;.JS;=;%%\r\n)\r\n\r\nendLocal & goto #_undefined_# 2>NUL || title %%COMSPEC%% & \"%%_prog%%\"  \"%%dp0%%\\%s\" %%*\r\n"

// cross-spawn escapes arguments twice for npm's node_modules\.bin shims,
// which re-parse them; a shim in npm's own format must still receive them
// exactly.
func TestRunCmdPassesArgumentsToNpmShimsExactly(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for an npm shim: %v", err)
	}
	root := filepath.Join(t.TempDir(), "project with spaces", "node_modules")
	bin := filepath.Join(root, ".bin")
	if err := os.MkdirAll(filepath.Join(root, "echoargs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "echoargs", "cli.js"), []byte("process.stdout.write(JSON.stringify(process.argv.slice(2)));\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(bin, "echoargs.cmd")
	if err := os.WriteFile(shim, fmt.Appendf(nil, npmShimTemplate, `..\echoargs\cli.js`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"install", "demo", "--prefix", `C:\Users\Jane Smith\.pig\agent\npm`},
		{`quote"inside`, "amp&pipe|lt<gt>", "caret^excl!", "paren(x)", "semi;comma,star*q?"},
	} {
		out, err := runCmd(shim, args...)
		if err != nil {
			t.Fatalf("runCmd(%q): %v", args, err)
		}
		var got []string
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("runCmd(%q) output %q: %v", args, out, err)
		}
		if !slices.Equal(got, args) {
			t.Fatalf("npm shim received %q, want %q", got, args)
		}
	}
}

// Upstream spawns package managers with cross-spawn on win32 (utils/
// child-process.ts spawnProcess), which runs a .cmd shim through cmd.exe with
// each argument escaped, so an npm.cmd under C:\Program Files receives
// arguments with spaces and cmd metacharacters exactly. Go's os/exec quotes
// for the C runtime only, and cmd.exe then misreads a spaced .cmd path.
func TestRunCmdPassesArgumentsToCmdShimsExactly(t *testing.T) {
	shim := buildArgsEcho(t)
	for _, args := range [][]string{
		{"install", "demo@1.0.0"},
		{"install", "demo", "--prefix", `C:\Users\Jane Smith\.pig\agent\npm`},
		{`quote"inside`, `trailing\`, `back\"slash`},
		{"amp&pipe|lt<gt>", "caret^pct%excl!", "paren(x)", "semi;comma,star*q?"},
		{""},
	} {
		out, err := runCmd(shim, args...)
		if err != nil {
			t.Fatalf("runCmd(%q): %v", args, err)
		}
		var got []string
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("runCmd(%q) output %q: %v", args, out, err)
		}
		if !slices.Equal(got, args) {
			t.Fatalf("shim received %q, want %q", got, args)
		}
	}
}
