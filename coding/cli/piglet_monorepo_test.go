package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func TestPigletAddPinnedMonorepoThroughCoreMaterializer(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	repo := shortTempDir(t)
	git := func(args ...string) string {
		t.Helper()
		out, err := packagemanager.RunCmdInDir(repo, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "piglet.yaml"), []byte("name: wrong-root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		root := filepath.Join(repo, "piglets", name)
		if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
			t.Fatal(err)
		}
		for path, content := range map[string]string{
			"piglet.yaml":            "name: " + name + "\nrelease: {version: 1.0.0}\nsystemPrompt: {file: prompt.md}\nskills:\n  - name: review\n    origins: [local:./skills/review]\n",
			"prompt.md":              name + " prompt\n",
			"skills/review/SKILL.md": "---\nname: review\ndescription: review\n---\n" + name + " skill\n",
		} {
			if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	git("add", "piglets", "piglet.yaml")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "fixture")
	commit := git("rev-parse", "HEAD")
	bare := filepath.Join(shortTempDir(t), "owner", "piglets.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := packagemanager.RunCmd("git", "clone", "--bare", repo, bare); err != nil {
		t.Fatal(err)
	}
	home := shortTempDir(t)
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	t.Chdir(t.TempDir())
	for _, name := range []string{"alpha", "beta"} {
		source := "git:file://localhost/" + strings.TrimPrefix(filepath.ToSlash(bare), "/") + "@" + commit + "#subdirectory=piglets%2F" + name
		var out, stderr strings.Builder
		if code := piglet.RunCommand([]string{"piglet", "add", source}, &out, &stderr); code != 0 {
			t.Fatalf("add %s code=%d %s %s", name, code, out.String(), stderr.String())
		}
		installed := filepath.Join(home, "piglets", name+".yaml")
		p, err := piglet.Parse(installed)
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != name {
			t.Fatalf("selected wrong Piglet: %s", p.Name)
		}
		prompt, err := os.ReadFile(filepath.Join(filepath.Dir(installed), p.SystemPrompt.File))
		if err != nil || string(prompt) != name+" prompt\n" {
			t.Fatalf("prompt=%q err=%v", prompt, err)
		}
		data, err := os.ReadFile(filepath.Join(home, "piglets", name+".origin.json"))
		if err != nil {
			t.Fatal(err)
		}
		var origin struct {
			Source string            `json:"source"`
			Commit string            `json:"commit"`
			Files  map[string]string `json:"files"`
		}
		if err := json.Unmarshal(data, &origin); err != nil {
			t.Fatal(err)
		}
		// The fixture declares exactly these two local inputs; the YAML stays at the registration root.
		closurePaths := []string{"prompt.md", "skills/review/SKILL.md"}
		if origin.Source != source || origin.Commit != commit || len(origin.Files) != len(closurePaths) {
			t.Fatalf("origin=%+v", origin)
		}
		for _, path := range closurePaths {
			if origin.Files[name+".source/"+commit+"/"+path] == "" {
				t.Fatalf("origin omits selected file %s", path)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "wrong-root.yaml")); !os.IsNotExist(err) {
		t.Fatalf("root agent registered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "agent", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("add changed Package settings: %v", err)
	}
	// The registered closures no longer depend on the mutable Package checkout.
	if err := os.RemoveAll(filepath.Join(home, "agent", "git")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		p, err := piglet.Parse(filepath.Join(home, "piglets", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		skills, errs := piglet.ResolveSkills(p)
		if len(errs) != 0 || len(skills) != 1 {
			t.Fatalf("skills=%v errs=%v", skills, errs)
		}
		data, err := os.ReadFile(filepath.Join(skills[0].Path, "SKILL.md"))
		if err != nil || !strings.HasSuffix(string(data), name+" skill\n") {
			t.Fatalf("skill=%q err=%v", data, err)
		}
	}
}
