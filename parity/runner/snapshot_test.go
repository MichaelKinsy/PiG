//go:build parity

package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSnapshotEnvDirsInjectsAuthWithoutMutatingFixture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pigAuthDir := filepath.Join(home, ".pig", "agent")
	if err := os.MkdirAll(pigAuthDir, 0o700); err != nil {
		t.Fatal(err)
	}
	auth := []byte(`{"github-copilot":{"type":"oauth","access":"test"}}`)
	authPath := filepath.Join(pigAuthDir, "auth.json")
	if err := os.WriteFile(authPath, auth, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_REAL_AUTH", authPath)
	fixtureRoot := t.TempDir()
	fixture := filepath.Join(fixtureRoot, "pi-agent")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(fixtureRoot, "scenario.toml")
	out := snapshotEnvDirs(t, scenario, []string{"PI_CODING_AGENT_DIR=pi-agent"}, true, true)
	_, snapshot, ok := strings.Cut(out[0], "=")
	if !ok {
		t.Fatalf("snapshot env = %q", out[0])
	}
	if got, err := os.ReadFile(filepath.Join(snapshot, "auth.json")); err != nil || string(got) != string(auth) {
		t.Fatalf("snapshot auth = %q, err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(fixture, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("fixture auth mutated: %v", err)
	}
}

// TestSnapshotEnvDirsNeverInjectsAmbientRealAuthWithoutOptIn is the regression
// guard for the privacy bug: a requires-auth scenario (inject=true) must not
// receive the operator's real ~/.pig/agent/auth.json unless PIG_PARITY_REAL_AUTH
// explicitly names it. Before the fix, EnsureTestdataAuth scanned $HOME/.pig
// and $HOME/.pi automatically, so this test failed (real creds present in the
// snapshot) on the old code.
func TestSnapshotEnvDirsNeverInjectsAmbientRealAuthWithoutOptIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_PARITY_REAL_AUTH", "")
	pigAuthDir := filepath.Join(home, ".pig", "agent")
	if err := os.MkdirAll(pigAuthDir, 0o700); err != nil {
		t.Fatal(err)
	}
	realCreds := []byte(`{"github-copilot":{"type":"oauth","access":"do-not-leak-me"}}`)
	if err := os.WriteFile(filepath.Join(pigAuthDir, "auth.json"), realCreds, 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureRoot := t.TempDir()
	fixture := filepath.Join(fixtureRoot, "pi-agent")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(fixtureRoot, "scenario.toml")
	out := snapshotEnvDirs(t, scenario, []string{"PI_CODING_AGENT_DIR=pi-agent"}, true, true)
	_, snapshot, ok := strings.Cut(out[0], "=")
	if !ok {
		t.Fatalf("snapshot env = %q", out[0])
	}
	if _, err := os.Stat(filepath.Join(snapshot, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("real credentials injected into a requires-auth snapshot without an opt-in: err=%v", err)
	}
}

func TestSnapshotEnvDirsDropsFixtureCredentialsUnlessRequired(t *testing.T) {
	fixtureRoot := t.TempDir()
	fixture := filepath.Join(fixtureRoot, "agent")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "auth.json"), []byte(`{"provider":{"access":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := snapshotEnvDirs(t, filepath.Join(fixtureRoot, "scenario.toml"), []string{"PI_CODING_AGENT_DIR=agent"}, false, false)
	_, snapshot, ok := strings.Cut(out[0], "=")
	if !ok {
		t.Fatalf("snapshot env = %q", out[0])
	}
	if _, err := os.Stat(filepath.Join(snapshot, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("credential copied into non-auth scenario: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture, "auth.json")); err != nil {
		t.Fatalf("fixture was mutated: %v", err)
	}
}

func TestSnapshotEnvDirsPreservesExplicitFixtureAuthWithoutAmbientInjection(t *testing.T) {
	fixtureRoot := t.TempDir()
	fixture := filepath.Join(fixtureRoot, "agent")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"provider":{"type":"oauth","refresh":"fixture"}}`)
	if err := os.WriteFile(filepath.Join(fixture, "auth.json"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	out := snapshotEnvDirs(t, filepath.Join(fixtureRoot, "scenario.toml"), []string{"PI_CODING_AGENT_DIR=agent"}, true, false)
	_, snapshot, _ := strings.Cut(out[0], "=")
	got, err := os.ReadFile(filepath.Join(snapshot, "auth.json"))
	if err != nil || string(got) != string(want) {
		t.Fatalf("fixture auth = %q, err=%v", got, err)
	}
}

func TestEveryDriverSnapshotsBinaryEnvironment(t *testing.T) {
	for _, name := range []string{
		"driver_cli.go",
		"driver_extension_host.go",
		"driver_ht.go",
		"driver_print.go",
		"driver_rpc.go",
		"driver_tmux.go",
	} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "snapshotBinaryEnv(") {
			t.Errorf("%s does not isolate BinaryRef environment directories", name)
		}
	}
}

func TestCopyDir_PreservesExecutableBit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	exe := filepath.Join(src, "tool.sh")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write source executable: %v", err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "tool.sh"))
	if err != nil {
		t.Fatalf("stat copied executable: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("copied file lost executable bit: mode=%#o", info.Mode().Perm())
	}
}

func TestRewriteSessionHeaderCwd_PreservesSnapshotRelativeTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := "{\"type\":\"session\",\"cwd\":\"{{SNAPSHOT}}/target\"}\n{\"type\":\"message\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "snapshot")
	if err := rewriteSessionHeaderCwd(path, root); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("rewritten session has no header")
	}
	var header struct {
		CWD string `json:"cwd"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "target"); header.CWD != want {
		t.Fatalf("cwd = %q, want %q", header.CWD, want)
	}
	if !scanner.Scan() || scanner.Text() != `{"type":"message"}` {
		t.Fatalf("body changed: %q", scanner.Text())
	}
}

func TestSnapshotExtensionPathIsWritableAndDoesNotMutateFixture(t *testing.T) {
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "extension.mjs")
	if err := os.WriteFile(source, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "helper.mjs"), []byte("helper"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotExtensionPath(t, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(snapshot), "helper.mjs")); err != nil {
		t.Fatalf("sibling import was not snapshotted: %v", err)
	}
	got, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("source fixture mutated through snapshot: %q", got)
	}
}

// trustSeedCheckout builds a minimal PiG checkout with one scenario and
// returns the scenario path and the canonical checkout root.
func trustSeedCheckout(t *testing.T) (scenario, root string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/MichaelKinsy/PiG\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	family := filepath.Join(root, "parity", "scenarios", "family")
	if err := os.MkdirAll(family, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(family, "01-scenario.toml"), root
}

func writeTrustFixture(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if body == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "trust.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTrustStore(t *testing.T, path string) map[string]*bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	store := map[string]*bool{}
	if err := json.Unmarshal(raw, &store); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return store
}

func TestSnapshotEnvDirsSeedsCheckoutUntrusted(t *testing.T) {
	scenario, root := trustSeedCheckout(t)
	family := filepath.Dir(scenario)
	writeTrustFixture(t, filepath.Join(family, "testdata", "pi-agent"), "")
	writeTrustFixture(t, filepath.Join(family, "testdata", "pig-home"), "")
	writeTrustFixture(t, filepath.Join(family, "testdata", "pi-home"), "")

	out := snapshotEnvDirs(t, scenario, []string{
		"PI_CODING_AGENT_DIR=testdata/pi-agent",
		"PIG_HOME=testdata/pig-home",
		"PI_HOME=testdata/pi-home",
	}, false, false)

	for i, trustPath := range []string{"trust.json", filepath.Join("agent", "trust.json")} {
		_, snapshot, _ := strings.Cut(out[i], "=")
		store := readTrustStore(t, filepath.Join(snapshot, trustPath))
		if len(store) != 1 || store[root] == nil || *store[root] {
			t.Errorf("%s: trust store = %v, want only %s=false", out[i], store, root)
		}
	}
	_, piHome, _ := strings.Cut(out[2], "=")
	if _, err := os.Stat(filepath.Join(piHome, "trust.json")); !os.IsNotExist(err) {
		t.Errorf("PI_HOME is not an agent dir but got a trust store: %v", err)
	}
	for _, fixture := range []string{"pi-agent", filepath.Join("pig-home", "agent")} {
		if _, err := os.Stat(filepath.Join(family, "testdata", fixture, "trust.json")); !os.IsNotExist(err) {
			t.Errorf("fixture %s was mutated: %v", fixture, err)
		}
	}
}

func TestSnapshotEnvDirsKeepsFixtureTrustDecisions(t *testing.T) {
	scenario, root := trustSeedCheckout(t)
	family := filepath.Dir(scenario)
	project := filepath.Join(root, "parity", "scenarios", "family", "testdata", "project")
	cases := []struct {
		name    string
		fixture string
		want    map[string]string
	}{
		{
			name:    "other paths are kept and the root is added",
			fixture: `{"` + project + `": true, "/elsewhere": null}`,
			want:    map[string]string{project: "true", "/elsewhere": "null", root: "false"},
		},
		{
			name:    "a fixture that decides the root wins",
			fixture: `{"` + root + `": true}`,
			want:    map[string]string{root: "true"},
		},
		{
			name:    "a decided ancestor already covers the root",
			fixture: `{"` + filepath.Dir(root) + `": true}`,
			want:    map[string]string{filepath.Dir(root): "true"},
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := filepath.Join("testdata", "agent-"+string(rune('a'+i)))
			writeTrustFixture(t, filepath.Join(family, rel), tc.fixture)
			out := snapshotEnvDirs(t, scenario, []string{"PIG_CODING_AGENT_DIR=" + rel}, false, false)
			_, snapshot, _ := strings.Cut(out[0], "=")
			store := readTrustStore(t, filepath.Join(snapshot, "trust.json"))
			got := map[string]string{}
			for path, decision := range store {
				got[path] = "null"
				if decision != nil {
					got[path] = strconv.FormatBool(*decision)
				}
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("trust store = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCLIDriverSnapshotCWDRunsOutsideTheFixture(t *testing.T) {
	scenario, root := trustSeedCheckout(t)
	project := filepath.Join(filepath.Dir(scenario), "testdata", "project")
	if err := os.MkdirAll(filepath.Join(project, ".pi", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	sc := &Scenario{
		Name:       "01-scenario",
		Driver:     "cli-mode",
		SourcePath: scenario,
		CLI:        CLIDriverConfig{CWD: "testdata/project", SnapshotCWD: true},
	}
	res := cliModeDriver{}.Run(context.Background(), t, BinaryRef{Label: "pig", Path: "/bin/pwd", Args: []string{"-P"}}, sc)
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("pwd: exit=%d err=%v output=%q", res.ExitCode, res.Err, res.Output)
	}
	cwd := strings.TrimSpace(res.Output)
	if cwd == "" || strings.HasPrefix(cwd+string(filepath.Separator), root+string(filepath.Separator)) {
		t.Fatalf("snapshot_cwd ran in %q, inside the checkout %s", cwd, root)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".pi", "extensions")); err != nil {
		t.Fatalf("snapshot lacks the fixture project: %v", err)
	}

	sc.CLI.SnapshotCWD = false
	res = cliModeDriver{}.Run(context.Background(), t, BinaryRef{Label: "pig", Path: "/bin/pwd", Args: []string{"-P"}}, sc)
	if got := strings.TrimSpace(res.Output); got != project {
		t.Fatalf("plain cwd ran in %q, want the fixture %s", got, project)
	}
}

// TestCLIFixtureProjectsRunOutsideCheckout guards the checkout trust seed. A
// cli-mode cwd that holds its own .pi or .pig resources is a project-trust
// fixture; inside the checkout, the seeded checkout decision would answer its
// trust question instead of the behavior the scenario asserts.
func TestCLIFixtureProjectsRunOutsideCheckout(t *testing.T) {
	scenarios, err := DiscoverScenarios(filepath.Join("..", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	projects := 0
	for _, sc := range scenarios {
		if sc.Driver != "cli-mode" || sc.CLI.CWD == "" || strings.Contains(sc.CLI.CWD, "{{") {
			continue
		}
		cwd := resolveScenarioCWD(sc.SourcePath, sc.CLI.CWD)
		for _, config := range []string{".pi", ".pig"} {
			if _, err := os.Stat(filepath.Join(cwd, config)); err != nil {
				continue
			}
			projects++
			if !sc.CLI.SnapshotCWD {
				t.Errorf("%s: cwd %s holds %s but runs inside the checkout; set snapshot_cwd = true", sc.SourcePath, sc.CLI.CWD, config)
			}
		}
	}
	if projects == 0 {
		t.Fatal("no cli-mode fixture project found; the guard no longer sees the project-trust scenarios")
	}
}
