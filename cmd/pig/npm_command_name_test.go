package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func npmCommandProbe(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "npm.log")
	t.Setenv("NPM_COMMAND_PROBE_LOG", log)
	writeStubScript(t, filepath.Join(bin, "npm"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$NPM_COMMAND_PROBE_LOG\"\nif [ \"$1\" = --version ]; then printf 'pnpm\\n'; fi\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// Pi package-manager.ts:1760-1764 strips only cmd/exe, preserves case and never executes the command.
func TestNpmCommandNameIsLexical(t *testing.T) {
	log := npmCommandProbe(t)
	var names []string
	for _, tc := range []struct {
		parts []string
		want  string
	}{{[]string{"npm"}, "npm"}, {[]string{"NPM.EXE"}, "NPM"}, {[]string{"pnpm.sh"}, "pnpm.sh"}, {[]string{"bun", "--"}, ""}, {[]string{"mise", "--", "npm", "--", "pnpm.cmd"}, "pnpm"}} {
		got := packagemanager.NpmCommandName(tc.parts)
		names = append(names, got)
		if got != tc.want {
			t.Errorf("name(%q)=%q want%q", tc.parts, got, tc.want)
		}
	}
	data, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("NPM_COMMAND_NAMES %s", data)
	if calls, err := os.ReadFile(log); !os.IsNotExist(err) {
		t.Fatalf("command classification executed processes: %q, error=%v", calls, err)
	}
}

// Pi package-manager.ts:1797-1828 selects install/remove arguments from the command spelling, not its version output.
func TestNpmCommandClassificationAtInstallAndRemove(t *testing.T) {
	log := npmCommandProbe(t)
	cwd, agent := t.TempDir(), t.TempDir()
	sm := codingagent.NewSettingsManager(cwd, agent)
	if err := packagemanager.InstallManagedNPM(cwd, sm.AgentDir(), sm, "npm:@scope/pkg", false); err != nil {
		t.Fatal(err)
	}
	if err := uninstallManagedNPM(cwd, sm, "npm:@scope/pkg", false); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	// Pi's getNpmInstallRoot is path.join(agentDir, "npm"), in the platform's spelling.
	root := filepath.Join(agent, "npm")
	want := "install @scope/pkg --prefix " + root + " --legacy-peer-deps\n" +
		"uninstall @scope/pkg --prefix " + root + " --legacy-peer-deps\n"
	if string(calls) != want {
		t.Fatalf("commands = %q, want %q", calls, want)
	}
}

func TestNpmCommandNameEmptyAndSuffixBoundaries(t *testing.T) {
	for _, tc := range []struct {
		parts []string
		want  string
	}{
		{nil, ""},
		{[]string{""}, ""},
		{[]string{"mise", "--", ""}, ""},
		{[]string{"/usr/local/bin/bun.CmD"}, "bun"},
		{[]string{"/usr/local/bin/NPM.ExE"}, "NPM"},
		{[]string{"npm.js"}, "npm.js"},
	} {
		if got := packagemanager.NpmCommandName(tc.parts); got != tc.want {
			t.Errorf("name(%q)=%q, want %q", tc.parts, got, tc.want)
		}
	}
}

func BenchmarkNpmCommandClassification(b *testing.B) {
	command := []string{"mise", "exec", "node@20", "--", "npm.cmd"}
	b.ReportAllocs()
	for b.Loop() {
		if got := packagemanager.NpmCommandName(command); got != "npm" {
			b.Fatal(got)
		}
	}
}
