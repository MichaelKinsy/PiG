package piglet

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
)

func TestAddMonorepoPinnedClosure(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	for _, name := range []string{"alpha", "beta"} {
		root := filepath.Join(repo, "piglets", name)
		if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"piglet.yaml": "name: " + name + "\nrelease: {version: 1.0.0}\ntools: []\nsystemPrompt: {file: prompt.md}\nskills:\n  - name: review\n    origins: [local:./skills/review]\n",
			"prompt.md":   name + " prompt\n", "skills/review/SKILL.md": "---\nname: review\ndescription: Review\n---\n" + name + " skill\n",
		}
		for path, data := range files {
			if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, data)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "-q")
	git("add", "piglets")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "core.autocrlf=false", "commit", "-qm", "fixture")
	commit := git("rev-parse", "HEAD")
	installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) {
		ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
		if err != nil {
			return "", err
		}
		return filepath.Join(repo, filepath.FromSlash(ref.GitSubdir)), nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	for _, name := range []string{"alpha", "beta"} {
		source := "git:https://github.com/acme/piglets.git@" + commit + "#subdirectory=piglets%2F" + name
		var out, stderr strings.Builder
		if code := RunCommand([]string{"piglet", "add", source}, &out, &stderr); code != 0 {
			t.Fatalf("add %s: code=%d %s", name, code, stderr.String())
		}
		path := filepath.Join(home, "piglets", name+".yaml")
		p, err := Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		if p.BuiltinTools == nil || len(*p.BuiltinTools) != 0 {
			t.Fatalf("explicit empty tool scope lost: %+v", p.BuiltinTools)
		}
		_, prompt, err := readPigletRelativeFile(path, p.SystemPrompt.File)
		if err != nil || string(prompt) != name+" prompt\n" {
			t.Fatalf("prompt=%q err=%v", prompt, err)
		}
		skills, errs := ResolveSkills(p)
		if len(errs) != 0 || len(skills) != 1 {
			t.Fatalf("skills=%v errs=%v", skills, errs)
		}
		if !strings.HasPrefix(skills[0].Path, home+string(filepath.Separator)) {
			t.Fatalf("source-bound skill: %s", skills[0].Path)
		}
		origin, err := readPigletOrigin(path)
		if err != nil || origin == nil || origin.Commit != commit || origin.Source != source {
			t.Fatalf("origin=%+v err=%v", origin, err)
		}
	}
	alphaPath := filepath.Join(home, "piglets", "alpha.yaml")
	alpha, err := Parse(alphaPath)
	if err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(filepath.Dir(alphaPath), alpha.SystemPrompt.File)
	if err := os.WriteFile(promptPath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPigletOrigin(alphaPath); err == nil || !strings.Contains(err.Error(), "digest does not match") {
		t.Fatalf("tampered closure=%v", err)
	}
	if err := os.WriteFile(promptPath, []byte("alpha prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "remove", "alpha", "--source"}, &out, &stderr); code != 0 {
		t.Fatalf("remove=%d %s", code, stderr.String())
	}
	if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
		t.Fatalf("removed source left its closure: %v", err)
	}
	if _, err := readPigletOrigin(filepath.Join(home, "piglets", "beta.yaml")); err != nil {
		t.Fatalf("removal affected sibling: %v", err)
	}
	for _, selector := range []string{"main", "v1.0.0", "abcdef"} {
		if _, err := resolvePigletAddSource("git:https://github.com/acme/piglets.git@"+selector+"#subdirectory=piglets%2Falpha", io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "full lowercase commit") {
			t.Fatalf("unpinned %s=%v", selector, err)
		}
	}
	wrongCommit := strings.Repeat("0", 40)
	if _, err := resolvePigletAddSource("git:https://github.com/acme/piglets.git@"+wrongCommit+"#subdirectory=piglets%2Falpha", io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "does not match pinned commit") {
		t.Fatalf("wrong commit=%v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "piglets", "alpha", "prompt.md"), []byte("modified checkout"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := "git:https://github.com/acme/piglets.git@" + commit + "#subdirectory=piglets%2Falpha"
	if _, err := resolvePigletAddSource(raw, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "modified or untracked") {
		t.Fatalf("dirty checkout=%v", err)
	}
	installresolver.SetMaterializer(func(_, _, _ string, _, _ io.Writer) (string, error) { return repo, nil })
	if _, err := resolvePigletAddSource(raw, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "does not match selected") {
		t.Fatalf("root fallback=%v", err)
	}
}

func TestUpdateCommandRoutesBeforeSession(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	var out, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "update", "--help"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "pig piglet update") {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), stderr.String())
	}
	for _, args := range [][]string{{"piglet", "update"}, {"piglet", "update", "alpha", "beta"}} {
		out.Reset()
		stderr.Reset()
		if code := RunCommand(args, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "Piglet name") || strings.Contains(stderr.String(), "pull") {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, stderr.String())
		}
	}
}
