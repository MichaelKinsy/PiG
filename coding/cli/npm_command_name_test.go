package cli

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

// .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:1793-1815 strips only cmd/exe, preserves case and never executes the command.
// Rows 1-5 are the 0.87.1 rows that still hold; `bun --` now names bun (the direct command) where 0.87.1 named "", and the
// last three are package-manager.test.ts:916 ("should prefer the package manager after a separator over the outer executable")
// and :938 (corepack wrapper without a separator), both regressions for #9863.
func TestPackageManagerNameIsLexical(t *testing.T) {
	log := npmCommandProbe(t)
	var names []string
	for _, tc := range []struct {
		parts []string
		want  string
	}{
		{[]string{"npm"}, "npm"}, {[]string{"NPM.EXE"}, "NPM"}, {[]string{"pnpm.sh"}, "pnpm.sh"},
		{[]string{"bun", "--"}, "bun"}, {[]string{"mise", "--", "npm", "--", "pnpm.cmd"}, "pnpm"},
		{[]string{"npm", "exec", "--", "pnpm"}, "pnpm"}, {[]string{"corepack", "pnpm"}, "pnpm"}, {[]string{"corepack", "pnpm", "pnpm.cmd"}, "pnpm"},
	} {
		got, err := packagemanager.PackageManagerName(tc.parts)
		if err != nil {
			t.Errorf("name(%q) failed: %v", tc.parts, err)
		}
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
	if err := packageManagerFor(cwd, sm, nil).Remove("npm:@scope/pkg", false); err != nil {
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

// .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:1780-1815: an absent or empty argv is npm, an empty first
// entry is an error, an empty wrapped command falls back to the direct command, and only npm, pnpm and bun are looked for among
// the arguments of an unsupported command (two different ones are ambiguous).
func TestPackageManagerNameEmptyAndSuffixBoundaries(t *testing.T) {
	for _, tc := range []struct {
		parts []string
		want  string
		err   string
	}{
		{nil, "npm", ""},
		{[]string{}, "npm", ""},
		{[]string{""}, "", "Invalid npmCommand: first array entry must be a non-empty command"},
		{[]string{"mise", "--", ""}, "mise", ""},
		{[]string{"/usr/local/bin/bun.CmD"}, "bun", ""},
		{[]string{"/usr/local/bin/NPM.ExE"}, "NPM", ""},
		{[]string{"npm.js"}, "npm.js", ""},
		{[]string{"mise", "exec"}, "mise", ""},
		{[]string{"corepack", "yarn"}, "corepack", ""},
		{[]string{"corepack", "/opt/pnpm.exe", "pnpm"}, "pnpm", ""},
		{[]string{"corepack", "pnpm", "npm"}, "", "Ambiguous npmCommand package managers: pnpm, npm"},
		{[]string{"corepack", "bun", "npm", "bun"}, "", "Ambiguous npmCommand package managers: bun, npm"},
		{[]string{"npm", "run", "--", "yarn"}, "yarn", ""},
	} {
		got, err := packagemanager.PackageManagerName(tc.parts)
		if (err == nil) != (tc.err == "") || (err != nil && err.Error() != tc.err) || got != tc.want {
			t.Errorf("name(%q)=%q, %v; want %q, %q", tc.parts, got, err, tc.want, tc.err)
		}
	}
}

func BenchmarkPackageManagerName(b *testing.B) {
	command := []string{"mise", "exec", "node@20", "--", "npm.cmd"}
	b.ReportAllocs()
	for b.Loop() {
		if got, err := packagemanager.PackageManagerName(command); err != nil || got != "npm" {
			b.Fatal(got, err)
		}
	}
}
