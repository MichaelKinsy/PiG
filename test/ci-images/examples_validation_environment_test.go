package ciimages

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// This exercises the maintained Make recipe, not a separately wrapped invocation. Compiler and validator probes isolate the recipe's environment/argument contract from language builds.
func TestExampleValidationRecipeIgnoresCallerConfiguration(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not on PATH")
	}
	goRootOutput, err := exec.CommandContext(t.Context(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	goRoot := strings.TrimSpace(string(goRootOutput))
	rootKeys := []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"}
	for _, piDirs := range []string{"", "1"} {
		t.Run("Pi directories="+piDirs, func(t *testing.T) {
			parent := t.TempDir()
			probeDir := filepath.Join(parent, "probes")
			if err := os.Mkdir(probeDir, 0o700); err != nil {
				t.Fatal(err)
			}
			probe := filepath.Join(probeDir, "pig-probe")
			rustRoot := filepath.Join(parent, "selected Rust")
			rustBin := filepath.Join(rustRoot, "bin")
			if err := os.MkdirAll(rustBin, 0o700); err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(parent, "calls.jsonl")
			program := `#!/usr/bin/env python3
import json, os, subprocess, sys
subprocess.run(["cargo", "--version"], check=True, stdout=subprocess.DEVNULL)
keys = ["HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "GOCACHE", "GOMODCACHE"]
with open(os.environ["VALIDATOR_CAPTURE"], "a") as output:
    output.write(json.dumps({"args": sys.argv[1:], "env": {key: os.environ.get(key, "") for key in keys}}) + "\n")
print(json.dumps({"valid": True, "diagnostics": []}))
`
			for path, contents := range map[string]string{
				probe:                            program,
				filepath.Join(probeDir, "cargo"): "#!/bin/sh\nif [ \"$HOME\" != \"$VALIDATOR_PARENT_HOME\" ]; then echo 'unresolved Cargo shim used' >&2; exit 98; fi\n",
				filepath.Join(probeDir, "rustc"): "#!/bin/sh\nprintf '%s\\n' \"$VALIDATOR_RUST_ROOT\"\n",
				filepath.Join(rustBin, "cargo"):  "#!/bin/sh\nexit 0\n",
			} {
				if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			lock := filepath.Join(parent, "trust.json.lock")
			const sentinel = "invoking user's regular-file trust lock"
			if err := os.WriteFile(lock, []byte(sentinel), 0o600); err != nil {
				t.Fatal(err)
			}
			cache, modules := filepath.Join(parent, "compiler cache"), filepath.Join(parent, "module cache")
			cmd := exec.CommandContext(t.Context(), makeBin, "--no-print-directory", "-o", "test-prereqs", "-o", "parity-bin", "examples-check", "PARITY_PIG_BIN="+probe)
			cmd.Dir = repoRoot(t)
			cmd.Env = append(os.Environ(), "PATH="+probeDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GOROOT="+goRoot, "GOCACHE="+cache, "GOMODCACHE="+modules,
				"TMPDIR="+parent, "PIG_USE_PI_DIRS="+piDirs, "VALIDATOR_CAPTURE="+capture,
				"VALIDATOR_PARENT_HOME="+parent, "VALIDATOR_RUST_ROOT="+rustRoot)
			for _, key := range rootKeys {
				cmd.Env = append(cmd.Env, key+"="+parent)
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("examples-check: %v\n%s", err, output)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			examples := []string{"go-factory", "rust-factory", "python-factory"}
			if len(lines) != len(examples) {
				t.Fatalf("validator calls = %s", data)
			}
			for i, line := range lines {
				var call struct {
					Args []string          `json:"args"`
					Env  map[string]string `json:"env"`
				}
				if err := json.Unmarshal([]byte(line), &call); err != nil {
					t.Fatal(err)
				}
				wantArgs := []string{"install", "--validate-only", "--json", "examples/extensions/" + examples[i]}
				if !slices.Equal(call.Args, wantArgs) {
					t.Errorf("validator args = %q, want %q", call.Args, wantArgs)
				}
				home := call.Env["HOME"]
				if home == parent || home == "" {
					t.Errorf("%s validator inherited the caller's HOME %q", examples[i], home)
					continue
				}
				for _, key := range rootKeys {
					rel, err := filepath.Rel(home, call.Env[key])
					if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						t.Errorf("%s=%q escapes isolated HOME %q", key, call.Env[key], home)
					}
				}
				if call.Env["GOCACHE"] != cache || call.Env["GOMODCACHE"] != modules {
					t.Errorf("compiler caches changed: %v", call.Env)
				}
				if _, err := os.Stat(home); !os.IsNotExist(err) {
					t.Errorf("validator HOME not removed: %s: %v", home, err)
				}
			}
			if data, err := os.ReadFile(lock); err != nil || string(data) != sentinel {
				t.Errorf("caller lock changed: %q, %v", data, err)
			}
		})
	}
}
