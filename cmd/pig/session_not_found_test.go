package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi main.ts:251-281 returns one not_found result for an unmatched ID/prefix; both session and fork print the same diagnostic at lines 385 and 409.
func TestMissingSessionArgumentDiagnostic(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	for _, flags := range []CLIFlags{{Session: "nosuch-id"}, {Fork: "nosuch-id"}, {Session: "no'such"}, {Fork: "no'such"}} {
		arg := flags.Session
		if arg == "" {
			arg = flags.Fork
		}
		_, err := resolveStartupSessionSelection(flags, t.TempDir(), t.TempDir())
		want := "No session found matching '" + arg + "'"
		if err == nil || err.Error() != want {
			t.Errorf("flags=%+v error=%v, want %q", flags, err, want)
		}
	}
}

// Compare complete stdout, stderr (including ANSI when forced), and exit status with the installed Pi 0.87.1. The error precedes model/provider startup in every mode.
func TestMissingSessionCLIComparedWithPi(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	piRoot, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "1.0.3" {
		t.Fatalf("Pi version=%q", pkg.Version)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--session", "--fork"} {
		for _, mode := range []string{"print", "json", "rpc"} {
			for _, color := range []string{"0", "1"} {
				t.Run(flag+"/"+mode+"/color="+color, func(t *testing.T) {
					home := t.TempDir()
					cwd := t.TempDir()
					t.Setenv("HOME", home)
					t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "pig"))
					t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi"))
					t.Setenv("FORCE_COLOR", color)
					t.Setenv("NO_COLOR", "")
					args := []string{flag, "nosuch-id"}
					if mode == "rpc" {
						args = append(args, "--mode", "rpc")
					} else {
						if mode == "json" {
							args = append(args, "--mode", "json")
						}
						args = append(args, "-p", "hi")
					}
					run := func(command string, argv []string) (string, string, int) {
						cmd := exec.CommandContext(t.Context(), command, argv...)
						cmd.Dir = cwd
						var stdout, stderr bytes.Buffer
						cmd.Stdout = &stdout
						cmd.Stderr = &stderr
						code := 0
						if err := cmd.Run(); err != nil {
							if exit, ok := errors.AsType[*exec.ExitError](err); ok {
								code = exit.ExitCode()
							} else {
								t.Fatal(err)
							}
						}
						return stdout.String(), stderr.String(), code
					}
					piOut, piErr, piCode := run(node, append([]string{filepath.Join(piRoot, "dist", "cli.js")}, args...))
					want := "No session found matching 'nosuch-id'"
					if color == "1" {
						want = "\x1b[31m" + want + "\x1b[39m"
					}
					want += "\n"
					if piOut != "" || piErr != want || piCode != 1 {
						t.Fatalf("Pi stdout=%q stderr=%q exit=%d", piOut, piErr, piCode)
					}
					pigOut, pigErr, pigCode := run(binary, args)
					if pigOut != piOut || pigErr != piErr || pigCode != piCode {
						t.Errorf("pig stdout=%q stderr=%q exit=%d; Pi stdout=%q stderr=%q exit=%d", pigOut, pigErr, pigCode, piOut, piErr, piCode)
					}
				})
			}
		}
	}
}
