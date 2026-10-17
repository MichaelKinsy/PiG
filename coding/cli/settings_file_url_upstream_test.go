package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// package-manager.ts:2329-2345 resolveLocalEntries resolves every plain entry of
// the settings skills/prompts/themes/extensions arrays with
// resolvePath(entry, baseDir, { trim: true }); fileURLToPath throws for an
// invalid file: URL and the throw fails resource loading. Pattern entries are
// not paths, so they never reach resolvePath.
func TestPackageContentResolveConfiguredRejectsInvalidFileURL(t *testing.T) {
	base := t.TempDir()
	for _, tc := range []struct {
		name    string
		entries []string
		wantErr bool
	}{
		{"encoded slash", []string{"file:///a%2Fb"}, true},
		{"trimmed encoded slash", []string{"  file:///a%2Fb  "}, true},
		{"after a valid entry", []string{"ok.md", "file:///a%2Fb"}, true},
		{"pattern entries are not paths", []string{"!file:///a%2Fb", "+file:///a%2Fb", "-file:///a%2Fb", "file:///*%2F"}, false},
		{"valid entries", []string{"ok.md", " ~/x ", fileURLOf(filepath.Join(base, "x"))}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := packagecontent.ResolveConfiguredWithError(tc.entries, base, packagecontent.Prompts)
			if tc.wantErr {
				if err == nil || err.Error() != invalidFileURLMessage() || got != nil {
					t.Fatalf("ResolveConfigured(%q) = %q, %v; want nil and %q", tc.entries, got, err, invalidFileURLMessage())
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveConfigured(%q) error = %v", tc.entries, err)
			}
		})
	}
}

// package-manager.ts:933-958 resolves the project arrays, then the user arrays,
// for each of extensions, skills, prompts, themes in that order, and the first
// throw wins. An untrusted project contributes no settings (main.ts:731-735).
func TestValidateConfiguredResourceEntries(t *testing.T) {
	const bad = "file:///a%2Fb"
	for _, tc := range []struct {
		name          string
		trusted       bool
		user, project map[string][]string
		wantErr       bool
	}{
		{"no entries", true, nil, nil, false},
		{"user skills", true, map[string][]string{"skills": {bad}}, nil, true},
		{"user prompts", true, map[string][]string{"prompts": {bad}}, nil, true},
		{"user themes", true, map[string][]string{"themes": {bad}}, nil, true},
		{"user extensions", true, map[string][]string{"extensions": {bad}}, nil, true},
		{"project skills", true, nil, map[string][]string{"skills": {bad}}, true},
		{"project extensions", true, nil, map[string][]string{"extensions": {bad}}, true},
		{"untrusted project is not read", false, nil, map[string][]string{"skills": {bad}}, false},
		{"untrusted project keeps user checks", false, map[string][]string{"themes": {bad}}, map[string][]string{"skills": {bad}}, true},
		{"patterns are skipped", true, map[string][]string{"skills": {"!" + bad, "-" + bad, "+" + bad}}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			agentDir := filepath.Join(t.TempDir(), "agent")
			writeSettingsArrays(t, filepath.Join(agentDir, "settings.json"), tc.user)
			writeSettingsArrays(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json"), tc.project)
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			err := validateConfiguredResourceEntries(cwd, agentDir, sm, tc.trusted)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("error = %v, want none", err)
				}
				return
			}
			if err == nil || err.Error() != invalidFileURLMessage() {
				t.Fatalf("error = %v, want %q", err, invalidFileURLMessage())
			}
		})
	}
}

// package-manager.ts:933-958 checks project before user for each resource kind, not all project arrays before all user arrays.
func TestConfiguredResourceErrorsFollowPiOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		user, project map[string][]string
		want          string
	}{
		{"scope within kind", map[string][]string{"extensions": {"file://%"}}, map[string][]string{"extensions": {"file:///a%2Fb"}}, invalidFileURLMessage()},
		{"kind before scope", map[string][]string{"extensions": {"file://%"}}, map[string][]string{"skills": {"file:///a%2Fb"}}, "Invalid URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			writeSettingsArrays(t, filepath.Join(agentDir, "settings.json"), tc.user)
			writeSettingsArrays(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json"), tc.project)
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
			if err := validateConfiguredResourceEntries(cwd, agentDir, sm, true); err == nil || err.Error() != tc.want {
				t.Fatalf("first error = %v, want %q", err, tc.want)
			}
		})
	}
}

func writeSettingsArrays(t *testing.T, path string, arrays map[string][]string) {
	t.Helper()
	if len(arrays) == 0 {
		return
	}
	data, err := json.Marshal(arrays)
	if err != nil {
		t.Fatal(err)
	}
	writeResourceLoaderFixture(t, path, string(data))
}

// main.ts:736-787: createAgentSessionServices rejects while loading resources,
// before any mode starts, so print, JSON and RPC startup all exit 1 with the
// bare error message and send no prompt or RPC response.
func TestStartupRejectsInvalidSettingsFileURL(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"print", []string{"--print", "hello"}, ""},
		{"json", []string{"--mode", "json", "hello"}, ""},
		{"rpc", []string{"--mode", "rpc"}, "{\"id\":\"s\",\"type\":\"get_state\"}\n"},
	} {
		for _, scope := range []string{"user", "project"} {
			t.Run(tc.name+"/"+scope, func(t *testing.T) {
				root := t.TempDir()
				cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					t.Fatal(err)
				}
				settings := filepath.Join(agentDir, "settings.json")
				if scope == "project" {
					settings = filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json")
				}
				writeResourceLoaderFixture(t, settings, `{"skills":["file:///a%2Fb"]}`)
				args := append([]string{"--no-session", "--approve"}, tc.args...)
				run := runPigStartup(t, binary, root, agentDir, cwd, tc.stdin, args...)
				var exitErr *exec.ExitError
				if !errors.As(run.err, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("err = %v, want exit 1\nstdout:%s\nstderr:%s", run.err, run.stdout, run.stderr)
				}
				if run.stderr != "Error: "+invalidFileURLMessage()+"\n" || run.stdout != "" {
					t.Fatalf("stdout=%q stderr=%q, want the bare message on stderr only", run.stdout, run.stderr)
				}
			})
		}
	}
}

// resource-loader.ts:382-385,549-560 resolves settings before loading even the pre-trust extension factories.
func TestInvalidSettingsFileURLStopsPreTrustFactories(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	marker := filepath.Join(root, "factory-ran")
	quoted, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	writeResourceLoaderFixture(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json"), `{}`)
	writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), `{"skills":["file:///a%2Fb"]}`)
	writeResourceLoaderFixture(t, filepath.Join(agentDir, "extensions", "probe.js"), `import {writeFileSync} from "node:fs"; export default function() { writeFileSync(`+string(quoted)+`, "ran"); }`)
	binary := buildPigBinaryForSignalTest(t)
	run := runPigStartup(t, binary, root, agentDir, cwd, "", "--no-session", "--print", "hello")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pre-trust factory ran before settings validation: %v", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(run.err, &exitErr) || exitErr.ExitCode() != 1 || run.stdout != "" || run.stderr != "Error: "+invalidFileURLMessage()+"\n" {
		t.Fatalf("startup = %v, stdout %q, stderr %q", run.err, run.stdout, run.stderr)
	}
	writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), `{}`)
	run = runPigStartup(t, binary, root, agentDir, cwd, "", "--no-session", "--print", "hello")
	if data, err := os.ReadFile(marker); err != nil || string(data) != "ran" {
		t.Fatalf("valid settings did not load the fixture factory: %v, %q; stderr %q", err, data, run.stderr)
	}
}

// interactive-mode.ts:6222-6255: /reload runs resourceLoader.reload(); its throw
// for an invalid settings file: URL becomes `Reload failed: <message>`. The
// snapshot provider carries that error to the reload step.
func TestReloadSnapshotReportsInvalidSettingsFileURL(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), `{"prompts":["file:///a%2Fb"]}`)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, Args{}, nil)()
	if snapshot.Err == nil || snapshot.Err.Error() != invalidFileURLMessage() {
		t.Fatalf("snapshot error = %v, want %q", snapshot.Err, invalidFileURLMessage())
	}
}

// Pi's resolve() collects an installed npm Package's resources outside any catch (package-manager.ts:1315-1327), after it computes the Package identities and before it resolves the settings entries (:933-958). A manifest glob that Node's globSync throws for, here a POSIX class beside an escaped ",", rejects startup with the SyntaxError; Pi 1.1.0 stops print mode with that message for this Package.
func TestNpmPackageManifestGlobThrowRejectsResolution(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	root := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "globprobe")
	writeResourceLoaderFixture(t, filepath.Join(root, "package.json"), `{"name":"globprobe","version":"1.0.0","pi":{"prompts":["prompts/[[:alpha:]]*,md"]}}`)
	writeResourceLoaderFixture(t, filepath.Join(root, "prompts", "a,md"), "A\n")
	writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), `{"packages":["npm:globprobe"],"skills":["file:///a%2Fb"]}`)
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	flags := "u"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		flags = "iu"
	}
	want := `Invalid regular expression: /^(?!\.)[\p{L}\p{Nl}][^/]*?\,md$/` + flags + `: Invalid escape`
	if err := validateConfiguredResourceEntries(cwd, agentDir, sm, true); err == nil || err.Error() != want {
		t.Fatalf("first error = %v, want %q", err, want)
	}
}

// Pi's resolve() applies the settings arrays' patterns with minimatch and checks every auto-discovered resource and built-in extension against their "!" entries (package-manager.ts:933-986, 718-737). A pattern minimatch cannot compile throws when a path first reaches it, so an earlier pattern that already matches every path keeps it from being compiled, and a project setting that decides a built-in keeps the user one from being read for it. Probed with Pi 1.1.0 (with --approve for the project cases): each case either rejects with this SyntaxError or starts.
func TestSettingsPatternErrorsFollowPi(t *testing.T) {
	const want = `Invalid regular expression: /^(?!\.)[\p{L}\p{Nl}]\-x$/u: Invalid escape`
	for _, tc := range []struct {
		name          string
		prompts       []string
		user, project string
		fails         bool
	}{
		{"auto-discovered prompt reaches the exclude", []string{"a"}, `{"prompts":["![[:alpha:]]-x"]}`, "", true},
		{"an earlier exclude matches every prompt", []string{"a"}, `{"prompts":["!a.md","![[:alpha:]]-x"]}`, "", false},
		{"a prompt the earlier exclude misses", []string{"a", "b"}, `{"prompts":["!a.md","![[:alpha:]]-x"]}`, "", true},
		{"a built-in extension reaches the exclude", nil, `{"extensions":["![[:alpha:]]-x"]}`, "", true},
		{"the project setting decides every built-in", nil, `{"extensions":["![[:alpha:]]-x"]}`, `{"extensions":["!builtin:*"]}`, false},
		{"the project setting decides no built-in", nil, `{"extensions":["![[:alpha:]]-x"]}`, `{"extensions":["!nothing-here"]}`, true},
		// An entry with "*" or "?" is a pattern over the files the plain entries collect (splitPatterns, resolveLocalEntries).
		{"an include pattern over the collected files", []string{"a"}, `{"prompts":["prompts","[[:alpha:]]-*"]}`, "", true},
		{"an entry without a glob character is a path", []string{"a"}, `{"prompts":["prompts","[[:alpha:]]-x"]}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			for _, name := range tc.prompts {
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "prompts", name+".md"), "x\n")
			}
			writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), tc.user)
			if tc.project != "" {
				writeResourceLoaderFixture(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json"), tc.project)
			}
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
			err := validateConfiguredResourceEntries(cwd, agentDir, sm, true)
			message := want
			if strings.HasSuffix(tc.user, `-*"]}`) {
				message = `Invalid regular expression: /^(?!\.)[\p{L}\p{Nl}]\-[^/]*?$/u: Invalid escape`
			}
			if tc.fails && (err == nil || err.Error() != message) || !tc.fails && err != nil {
				t.Fatalf("error = %v, want failure %t (%q)", err, tc.fails, message)
			}
		})
	}
}
