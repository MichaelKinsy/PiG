package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi package-manager.ts:1511-1519 selects npmCommand and runs view in this.cwd.
// The approved security policy changes only user-package lookup cwd to managed storage.
func TestNpmMetadataLookupScope(t *testing.T) {
	for _, manager := range []string{"npm", "pnpm", "bun"} {
		for _, local := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/project=%t", manager, local), func(t *testing.T) {
				cwd, sm, record := npmMetadataFixture(t, manager, local)
				// The configured project cwd, not the process cwd, owns trusted project lookups.
				t.Chdir(t.TempDir())
				updates := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: cwd, SettingsManager: sm}).CheckForAvailableUpdates()
				if len(updates) != 1 || updates[0].Source != "npm:fake-package" {
					t.Fatalf("updates = %#v", updates)
				}
				calls := readMetadataCalls(t, record)
				wantDir := filepath.Join(sm.AgentDir(), "npm")
				if local {
					wantDir = cwd
				}
				if len(calls) != 1 || canonicalTestPath(t, calls[0].CWD) != canonicalTestPath(t, wantDir) || !slices.Equal(calls[0].Args, []string{"selected-argument", "--", manager, "view", "fake-package", "version", "--json"}) {
					t.Fatalf("metadata calls = %#v, want selected %s command from %s", calls, manager, wantDir)
				}
			})
		}
	}
}

// Pi package-manager.ts:1150-1164 checks metadata before installation; lookup failures retain its install fallback.
func TestPackageUpdatePerformsScopedMetadataLookup(t *testing.T) {
	for _, local := range []bool{false, true} {
		for _, tc := range []struct {
			name, response string
			install        bool
		}{
			{"same", `"1.0.0"`, false},
			{"older", `"0.9.0"`, false},
			{"newer", `"2.0.0"`, true},
			{"bad-response", `{}`, true},
		} {
			t.Run(fmt.Sprintf("%s/project=%t", tc.name, local), func(t *testing.T) {
				cwd, sm, record := npmMetadataFixture(t, "npm", local)
				t.Chdir(cwd)
				t.Setenv("NPM_METADATA_RESPONSE", tc.response)
				if local {
					if err := codingagent.NewProjectTrustStore(sm.AgentDir()).Set(cwd, new(true)); err != nil {
						t.Fatal(err)
					}
				}
				_, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"update", "npm:fake-package"}) })
				if code != 0 {
					t.Fatalf("update exit=%d stderr=%s", code, stderr)
				}
				calls := readMetadataCalls(t, record)
				wantDir := filepath.Join(sm.AgentDir(), "npm")
				if local {
					wantDir = cwd
				}
				wantCalls := 1
				if tc.install {
					wantCalls++
				}
				if len(calls) != wantCalls || !slices.Contains(calls[0].Args, "view") || canonicalTestPath(t, calls[0].CWD) != canonicalTestPath(t, wantDir) {
					t.Fatalf("calls = %#v, want lookup from %s followed by install=%t", calls, wantDir, tc.install)
				}
				if tc.install && !slices.Contains(calls[1].Args, "install") {
					t.Fatalf("lookup must finish before install: %#v", calls)
				}
			})
		}
	}
}

func TestPackageMetadataOfflinePolicy(t *testing.T) {
	// Pi package-manager.ts:53-57 treats only 1, true and yes as offline; values are not trimmed.
	for _, tc := range []struct {
		value   string
		offline bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"1", true}, {"TRUE", true}, {"yes", true}, {" 1 ", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cwd, sm, record := npmMetadataFixture(t, "npm", false)
			t.Setenv("PI_OFFLINE", tc.value)
			if err := packageManagerFor(cwd, sm, nil).Update("npm:fake-package"); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(record)
			if tc.offline {
				if !os.IsNotExist(err) {
					t.Fatalf("offline update ran a command: %v", err)
				}
			} else if err != nil {
				t.Fatalf("online update skipped metadata: %v", err)
			}
		})
	}
}

func TestLatestNpmVersionFromJSON(t *testing.T) {
	for _, tc := range []struct{ response, versionRange, want string }{
		{`"2.0.0"`, "", "2.0.0"},
		{`["1.0.0", "2.0.0", "1.5.0"]`, "", "2.0.0"},
		{`[null, 7, "", "1.2.0", "2.0.0", "1.5.0"]`, "^1.0.0", "1.5.0"},
		{`["2.0.0-beta.1", "1.0.0"]`, "beta", "2.0.0-beta.1"},
		{`["2.0.0"]`, "^1.0.0", ""},
		{`[]`, "", ""},
		{`null`, "", ""},
		{" null\n", "", ""},
		{`{}`, "", ""},
		{"", "", ""},
	} {
		t.Run(tc.response+tc.versionRange, func(t *testing.T) {
			got, err := packagemanager.LatestNpmVersionFromJSON([]byte(tc.response), tc.versionRange)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("version=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestNpmMetadataLookupRefusesUnavailableRootAndUntrustedProject(t *testing.T) {
	cwd, sm, record := npmMetadataFixture(t, "npm", false)
	ref, err := packagemanager.ParseNpmInstallRef("npm:fake-package")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packagemanager.GetLatestNpmVersion(cwd, sm, ref, true); err == nil {
		t.Fatal("lookup allowed an untrusted project")
	}
	root := filepath.Join(sm.AgentDir(), "npm")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	writeNpmTrustFile(t, root, []byte("not a directory"))
	if _, err := packagemanager.GetLatestNpmVersion(cwd, sm, ref, false); err == nil {
		t.Fatal("lookup ignored unavailable managed storage")
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("lookup fell back to invoking cwd: %v", err)
	}
}

func TestNpmMetadataLookupCreatesManagedRootForLegacyInstall(t *testing.T) {
	cwd, sm, record := npmMetadataFixture(t, "npm", false)
	root := filepath.Join(sm.AgentDir(), "npm")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	ref, err := packagemanager.ParseNpmInstallRef("npm:fake-package")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packagemanager.GetLatestNpmVersion(cwd, sm, ref, false); err != nil {
		t.Fatal(err)
	}
	calls := readMetadataCalls(t, record)
	if len(calls) != 1 || canonicalTestPath(t, calls[0].CWD) != canonicalTestPath(t, root) {
		t.Fatalf("calls=%#v", calls)
	}
	if _, err := os.Stat(filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkNpmMetadataLookup(b *testing.B) {
	cwd, agentDir := b.TempDir(), b.TempDir()
	node, err := exec.LookPath("node")
	if err != nil {
		b.Fatal(err)
	}
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	sm.ApplyOverrides(codingagent.Settings{NpmCommand: []string{node, "-e", `process.stdout.write('"1.0.0"')`, "--"}})
	ref, err := packagemanager.ParseNpmInstallRef("npm:fake-package")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := packagemanager.GetLatestNpmVersion(cwd, sm, ref, false); err != nil {
			b.Fatal(err)
		}
	}
}

// npmMetadataCall.CWD is the child's process.cwd(), which getcwd reports as
// the physical directory (macOS /var is /private/var), so compare it through
// canonicalTestPath.
type npmMetadataCall struct {
	CWD  string   `json:"cwd"`
	Args []string `json:"args"`
}

func readMetadataCalls(t *testing.T, record string) []npmMetadataCall {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var calls []npmMetadataCall
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var call npmMetadataCall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}

func npmMetadataFixture(t *testing.T, manager string, local bool) (string, *codingagent.SettingsManager, string) {
	t.Helper()
	cwd, agentDir, record := npmTrustFixture(t)
	// Block the old literal-npm path from reaching a public registry in the red test.
	bin := t.TempDir()
	writeStubScript(t, filepath.Join(bin, "npm"), "#!/bin/sh\nprintf '\"2.0.0\"\\n'\n")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NPM_METADATA_RESPONSE", `"2.0.0"`)
	script := filepath.Join(agentDir, "metadata.cjs")
	writeNpmTrustFile(t, script, []byte(`const fs = require('node:fs');
const [record, ...args] = process.argv.slice(2);
fs.appendFileSync(record, JSON.stringify({cwd:process.cwd(), args})+'\n');
if(args.includes('view')) process.stdout.write(process.env.NPM_METADATA_RESPONSE);
`))
	global := map[string]any{"npmCommand": []string{node, script, record, "selected-argument", "--", manager}}
	project := map[string]any{}
	installRoot := filepath.Join(agentDir, "npm")
	if local {
		project["packages"] = []string{"npm:fake-package"}
		installRoot = filepath.Join(cwd, ".pig", "npm")
	} else {
		global["packages"] = []string{"npm:fake-package"}
	}
	for path, settings := range map[string]map[string]any{filepath.Join(agentDir, "settings.json"): global, filepath.Join(cwd, ".pig", "settings.json"): project} {
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		writeNpmTrustFile(t, path, data)
	}
	writeNpmTrustFile(t, filepath.Join(installRoot, "package.json"), []byte(`{"private":true}`))
	writeNpmTrustFile(t, filepath.Join(installRoot, "node_modules", "fake-package", "package.json"), []byte(`{"name":"fake-package","version":"1.0.0"}`))
	return cwd, codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, local), record
}
