package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A Piglet found by name in `<project>/.pig/piglets` is project config that can name extensions, a system prompt, a model and secrets, so it follows
// project trust like every other project resource. Before the fix a cloned repository's `.pig/piglets/<name>.yaml` shadowed the user's Piglet of
// that name with no prompt and no flag.
func TestResolveSkipsAnUntrustedWorkspacePigletAndPrefersTheUsers(t *testing.T) {
	home := t.TempDir()
	agentDir := t.TempDir()
	project := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv(codingagent.ENV_AGENT_DIR, agentDir)
	t.Chdir(project)
	userPiglet := filepath.Join(codingagent.PigletsDir(), "demo.yaml")
	workspacePiglet := filepath.Join(codingagent.ProjectConfigDir(project), "piglets", "demo.yaml")
	for _, path := range []string{userPiglet, workspacePiglet} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("name: demo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Resolve("demo")
	if err != nil || !samePath(got, userPiglet) {
		t.Fatalf("untrusted project: Resolve = %q, %v; want the user's %s", got, err, userPiglet)
	}

	if err := codingagent.NewProjectTrustStore(agentDir).Set(project, new(true)); err != nil {
		t.Fatal(err)
	}
	got, err = Resolve("demo")
	if err != nil || !samePath(got, workspacePiglet) {
		t.Fatalf("trusted project: Resolve = %q, %v; want the workspace's %s", got, err, workspacePiglet)
	}

	if got, err = ResolveWithTrust("demo", new(false)); err != nil || !samePath(got, userPiglet) {
		t.Fatalf("--no-approve: Resolve = %q, %v; want the user's %s", got, err, userPiglet)
	}
}

func TestResolveNamesAnIgnoredUntrustedWorkspacePiglet(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv(codingagent.ENV_AGENT_DIR, t.TempDir())
	project := t.TempDir()
	t.Chdir(project)
	path := filepath.Join(codingagent.ProjectConfigDir(project), "piglets", "only.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("name: only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve("only")
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("Resolve error = %v; want it to say the workspace Piglet is ignored because the project is not trusted", err)
	}
}

// Startup trusts an undecided project when the global defaultProjectTrust setting is "always" (coding/cli/project_trust.go resolveProjectTrusted),
// so a workspace Piglet does too; a stored "no" for the project still wins over the setting.
func TestResolveTrustsAWorkspacePigletUnderDefaultProjectTrustAlways(t *testing.T) {
	agentDir := t.TempDir()
	project := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv(codingagent.ENV_AGENT_DIR, agentDir)
	t.Chdir(project)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProjectTrust":"always"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	workspacePiglet := filepath.Join(codingagent.ProjectConfigDir(project), "piglets", "demo.yaml")
	if err := os.MkdirAll(filepath.Dir(workspacePiglet), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePiglet, []byte("name: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve("demo"); err != nil || !samePath(got, workspacePiglet) {
		t.Fatalf("defaultProjectTrust always: Resolve = %q, %v; want the workspace's %s", got, err, workspacePiglet)
	}
	if err := codingagent.NewProjectTrustStore(agentDir).Set(project, new(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve("demo"); err == nil {
		t.Fatal("a stored distrust decision must win over defaultProjectTrust always")
	}
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// The startup trust prompt names a Piglet the project defines under the name the run selected and that project trust withheld, so the user sees what
// trusting the project would change.
func TestShadowedWorkspacePigletNamesWhatTrustWithholds(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv(codingagent.ENV_AGENT_DIR, t.TempDir())
	project := t.TempDir()
	t.Chdir(project)
	path := filepath.Join(codingagent.ProjectConfigDir(project), "piglets", "demo.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("name: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ShadowedWorkspacePiglet("demo", nil); got != "demo" {
		t.Fatalf("untrusted: ShadowedWorkspacePiglet = %q, want demo", got)
	}
	if got := ShadowedWorkspacePiglet("demo", new(true)); got != "" {
		t.Fatalf("approved: ShadowedWorkspacePiglet = %q, want none", got)
	}
	if got := ShadowedWorkspacePiglet("other", nil); got != "" {
		t.Fatalf("a Piglet the project does not define: %q, want none", got)
	}
	// A value with a separator or a .yaml/.yml suffix is a path that ResolveWithTrust uses as given, never a workspace lookup: --piglet demo.yaml
	// selects the file demo.yaml in the working directory, so trust replaces nothing.
	for _, explicit := range []string{"./demo.yaml", "demo.yaml", "demo.yml"} {
		if got := ShadowedWorkspacePiglet(explicit, nil); got != "" {
			t.Fatalf("an explicit path %q is never shadowed: %q, want none", explicit, got)
		}
	}
	if got := ShadowedWorkspacePiglet("", nil); got != "" {
		t.Fatalf("no Piglet selected: %q, want none", got)
	}
}
