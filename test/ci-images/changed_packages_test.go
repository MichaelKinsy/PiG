package ciimages

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// changed-packages.py selects the packages that hold, embed or name a changed file, then every package whose tests import one of them; go.mod changes select everything, and a file no package reads is reported as unmapped.
func TestChangedPackagesSelectsTheReverseClosure(t *testing.T) {
	repo := t.TempDir()
	git := func(t *testing.T, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	files := map[string]string{
		"go.mod":                   "module example.com/m\n\ngo 1.26\n",
		"a/a.go":                   "package a\n\nfunc A() int { return 1 }\n",
		"b/b.go":                   "package b\n\nimport \"example.com/m/a\"\n\nfunc B() int { return a.A() }\n",
		"c/c.go":                   "package c\n\nfunc C() {}\n",
		"c/c_test.go":              "package c_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/b\"\n)\n\nfunc TestC(t *testing.T) { _ = b.B() }\n",
		"d/d.go":                   "package d\n\nimport _ \"embed\"\n\n//go:embed data.txt\nvar Data string\n",
		"d/data.txt":               "one\n",
		"e/e.go":                   "package e\n",
		"e/e_test.go":              "package e\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestE(t *testing.T) { _, _ = os.ReadFile(\"../docs/spec/rules/table.md\") }\n",
		"e/testdata/case.json":     "{}\n",
		"g/g.go":                   "package g\n",
		"g/g_test.go":              "package g\n\nimport (\n\t\"os/exec\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestG(t *testing.T) { _ = exec.Command(\"sh\", filepath.Join(\"..\", \"scripts\", \"tool\", \"run.sh\")) }\n",
		"scripts/tool/run.sh":      "true\n",
		"h/h.go":                   "package h\n\n// Taskfile names the tasks.\nvar Names = []string{\"Taskfile\"}\n",
		"i/i_test.go":              "package i\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestI(t *testing.T) { _, _ = os.ReadFile(filepath.Join(\"..\", \"Taskfile\")) }\n",
		"Taskfile":                 "x\n",
		"docs/spec/rules/table.md": "x\n",
		"docs/notes.md":            "x\n",
		"tagged/t.go":              "//go:build parity\n\npackage tagged\n\nimport \"example.com/m/a\"\n\nvar _ = a.A\n",
		"root.go":                  "package m\n\nimport \"embed\"\n\n//go:embed *.md\nvar Notes embed.FS\n",
		"NOTES.md":                 "x\n",
		"f/f.go":                   "package f\n\nimport \"example.com/m\"\n\nvar F = m.Notes\n",
	}
	for path, content := range files {
		writeCIFixture(t, repo, path, content)
	}
	git(t, "init", "-q")
	git(t, "add", ".")
	git(t, "commit", "-q", "-m", "base")
	git(t, "tag", "base")
	script := repoScript(t, "automation/ci/changed-packages.py")
	selection := func(t *testing.T, edits map[string]string) map[string][]string {
		t.Helper()
		git(t, "checkout", "-q", "-f", "base")
		git(t, "clean", "-qfd")
		for path, content := range edits {
			writeCIFixture(t, repo, path, content)
		}
		cmd := exec.CommandContext(t.Context(), hostPython(), script, "--base", "base")
		cmd.Dir = repo
		cmd.Env = append(cmd.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		out, err := cmd.Output()
		if err != nil {
			var stderr []byte
			if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
				stderr = exit.Stderr
			}
			t.Fatalf("changed-packages.py: %v\n%s%s", err, out, stderr)
		}
		got := map[string][]string{}
		for line := range strings.Lines(string(out)) {
			kind, entry, _ := strings.Cut(strings.TrimSpace(line), " ")
			got[kind] = append(got[kind], strings.TrimPrefix(entry, "example.com/m/"))
		}
		return got
	}
	for _, tc := range []struct {
		name  string
		edits map[string]string
		want  map[string][]string
	}{
		{"go file", map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"},
			map[string][]string{"direct": {"a"}, "affected": {"a", "b", "c"}, "tagged": {"tagged"}}},
		{"embedded file", map[string]string{"d/data.txt": "two\n"},
			map[string][]string{"direct": {"d"}, "affected": {"d"}}},
		{"testdata file", map[string]string{"e/testdata/case.json": "[]\n"},
			map[string][]string{"direct": {"e"}, "affected": {"e"}}},
		{"file a test names", map[string]string{"docs/spec/rules/table.md": "y\n"},
			map[string][]string{"direct": {"e"}, "affected": {"e"}}},
		// h names the root file Taskfile only in prose and as a word, which also names files of other directories; only i's path to it selects.
		{"root file a test joins", map[string]string{"Taskfile": "y\n"},
			map[string][]string{"direct": {"i"}, "affected": {"i"}}},
		{"file a test joins from its components", map[string]string{"scripts/tool/run.sh": "false\n"},
			map[string][]string{"direct": {"g"}, "affected": {"g"}}},
		// The module root's own directory reaches a file only through an embed.
		{"file the root package embeds", map[string]string{"NOTES.md": "y\n"},
			map[string][]string{"direct": {"example.com/m"}, "affected": {"example.com/m", "f"}}},
		{"file nothing reads", map[string]string{"docs/notes.md": "y\n"},
			map[string][]string{"unmapped": {"docs/notes.md"}}},
		{"untracked go file", map[string]string{"c/extra.go": "package c\n\nfunc Extra() {}\n"},
			map[string][]string{"direct": {"c"}, "affected": {"c"}}},
		{"go.mod", map[string]string{"go.mod": "module example.com/m\n\ngo 1.26.0\n"},
			map[string][]string{"affected": {"example.com/m", "a", "b", "c", "d", "e", "f", "g", "h", "i"}, "tagged": {"tagged"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := selection(t, tc.edits)
			for _, kind := range []string{"direct", "affected", "tagged", "unmapped"} {
				if !slices.Equal(got[kind], tc.want[kind]) {
					t.Errorf("%s = %v, want %v", kind, got[kind], tc.want[kind])
				}
			}
		})
	}
}
