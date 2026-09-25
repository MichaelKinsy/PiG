package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goToolchain returns the go.mod toolchain version, the one source for the
// Go pin (docs/project/compliance.md "One source for each version pin").
func goToolchain(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindSubmatch(data)
	if match == nil {
		t.Fatal("go.mod has no toolchain directive")
	}
	return string(match[1])
}

func TestGoVersionPolicy(t *testing.T) {
	root := repoRoot(t)
	goVersion := goToolchain(t, root)
	buildPins := map[string]string{
		"automation/images/ci-go/Dockerfile":        "ARG GO_IMAGE=golang:" + goVersion + "-",
		"automation/images/ci-parity/Dockerfile":    "ARG GO_VERSION=" + goVersion + "\n",
		"docs/site/docs/containerization.md":        "FROM golang:" + goVersion + " AS build",
		"docs/site/docs/termux.md":                  "Use Go " + goVersion + " for this build.",
		"docs/site/docs/windows.md":                 "Install Git and Go " + goVersion + ".",
		"internal/pigdocs/content/extension-api.md": "Build PiG and Go extensions with Go " + goVersion + ".",
	}
	for path, want := range buildPins {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not contain %q", path, want)
		}
	}
	checkWorkflowsReadGoMod(t, root)

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root {
			switch entry.Name() {
			case ".git", ".upstream", ".next", "node_modules", "out":
				return filepath.SkipDir
			}
		}
		if entry.IsDir() || entry.Name() != "go.mod" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "go ") {
				continue
			}
			if line != "go 1.26" && line != "go 1.26.0" {
				t.Errorf("%s declares %q; want Go 1.26 language floor", path, line)
			}
			return nil
		}
		t.Errorf("%s has no go directive", path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// checkWorkflowsReadGoMod requires every actions/setup-go step to take its
// version from go.mod and forbids any workflow-level Go version literal.
func checkWorkflowsReadGoMod(t *testing.T, root string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`^\s*(?:go-version|GO_VERSION):`)
	setupGo := regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*actions/setup-go@`)
	steps := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if literal.MatchString(line) {
				t.Errorf("%s:%d pins Go by literal %q; read go.mod with go-version-file", name, i+1, strings.TrimSpace(line))
			}
			if !setupGo.MatchString(line) {
				continue
			}
			steps++
			if !stepReadsGoMod(lines, i) {
				t.Errorf("%s:%d setup-go step does not set go-version-file: go.mod", name, i+1)
			}
		}
	}
	if steps == 0 {
		t.Fatal("no workflow uses actions/setup-go")
	}
}

// stepReadsGoMod reports whether the workflow step containing lines[index]
// sets go-version-file: go.mod. A step runs from its "- " line to the next
// line indented no deeper than that dash.
func stepReadsGoMod(lines []string, index int) bool {
	indent := func(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }
	start := index
	for start > 0 && !strings.HasPrefix(strings.TrimSpace(lines[start]), "- ") {
		start--
	}
	for i, line := range lines[start:] {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if i > 0 && trimmed != "" && indent(line) <= indent(lines[start]) {
			return false
		}
		if trimmed == "go-version-file: go.mod" {
			return true
		}
	}
	return false
}
