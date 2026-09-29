package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func TestCLIErrorRendering(t *testing.T) {
	for _, color := range []bool{false, true} {
		var out bytes.Buffer
		writeCLIError(&out, "failed", color)
		want := "Error: failed\n"
		if color {
			want = "\x1b[31mError: failed\x1b[39m\n"
		}
		if out.String() != want {
			t.Fatalf("got %q want %q", out.String(), want)
		}
	}
}

// Pi's spawnCommand routes both child streams to stderr when stdout belongs to print/RPC.
func TestPackageChildOutputDuringStdoutTakeover(t *testing.T) {
	script := writeStubScript(t, filepath.Join(t.TempDir(), "child"), "#!/bin/sh\nprintf 'out\\n'\nprintf 'err\\n' >&2\n")
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		if err := codingagent.TakeOverStdout(); err != nil {
			t.Fatal(err)
		}
		defer codingagent.RestoreStdout()
		if err := packagemanager.RunPackageProcess("", script); err != nil {
			t.Fatal(err)
		}
		return 0
	})
	if code != 0 || stdout != "" || stderr != "out\nerr\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestPackageSourceReplacementPreservesFilters(t *testing.T) {
	for _, local := range []bool{false, true} {
		cwd, agentDir := t.TempDir(), t.TempDir()
		sm := codingagent.NewSettingsManager(cwd, agentDir)
		set := sm.SetPackages
		if local {
			set = sm.SetProjectPackages
		}
		for _, source := range []string{"npm:@scope/pkg@v1", "git:github.com/user/repo@v1"} {
			if err := set([]codingagent.PackageSource{{Source: source, Extensions: []string{"extensions/*.ts"}, WasObject: true}}); err != nil {
				t.Fatal(err)
			}
			replacement := strings.TrimSuffix(source, "v1") + "v2"
			if _, err := addSourceToSettings(cwd, sm, replacement, local); err != nil {
				t.Fatal(err)
			}
			sm.Reload()
			got := sm.GetGlobalSettings().Packages
			if local {
				got = sm.GetProjectSettings().Packages
			}
			if len(got) != 1 || got[0].Source != replacement || !got[0].WasObject || len(got[0].Extensions) != 1 || got[0].Extensions[0] != "extensions/*.ts" {
				t.Fatalf("replacement lost source/filter: %#v", got)
			}
		}
	}
}

// The child emits 4 MiB; the parent inherits a file descriptor instead of accumulating child output.
func BenchmarkPackageInheritedOutput(b *testing.B) {
	root := b.TempDir()
	script := filepath.Join(root, "npm.cjs")
	if err := os.WriteFile(script, []byte("const b=Buffer.alloc(65536,120); for(let i=0;i<64;i++)require('node:fs').writeSync(1,b);"), 0o600); err != nil {
		b.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(root, filepath.Join(root, "agent"))
	if err := sm.SetNpmCommand([]string{"node", script}); err != nil {
		b.Fatal(err)
	}
	discard, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = discard
	defer func() { os.Stdout = previous; _ = discard.Close() }()
	b.ReportAllocs()
	b.SetBytes(4 * 1024 * 1024)
	for b.Loop() {
		if err := packagemanager.InstallManagedNPM(root, sm.AgentDir(), sm, "npm:fixture", false); err != nil {
			b.Fatal(err)
		}
	}
}
