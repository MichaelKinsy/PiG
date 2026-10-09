package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// goToolchain returns the .go-version pin, the one source for the Go version
// that CI, the images and the documented setup use
// (docs/project/compliance.md "One source for each version pin"). The go.mod
// and go.work toolchain lines stay at the oldest release the module builds
// with, so a Termux Go that cannot download a newer android toolchain still
// builds.
func goToolchain(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".go-version"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// moduleToolchain returns the toolchain directive of a go.mod or go.work file.
func moduleToolchain(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindSubmatch(data)
	if match == nil {
		t.Fatalf("%s has no toolchain directive", name)
	}
	return string(match[1])
}

// versionAtMost reports whether the dotted release version a is no newer than b.
func versionAtMost(t *testing.T, a, b string) bool {
	t.Helper()
	parse := func(v string) []int {
		var parts []int
		for field := range strings.SplitSeq(v, ".") {
			n, err := strconv.Atoi(field)
			if err != nil {
				t.Fatalf("version %q is not dotted numbers", v)
			}
			parts = append(parts, n)
		}
		return parts
	}
	x, y := parse(a), parse(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		var xi, yi int
		if i < len(x) {
			xi = x[i]
		}
		if i < len(y) {
			yi = y[i]
		}
		if xi != yi {
			return xi < yi
		}
	}
	return true
}

func TestGoVersionPolicy(t *testing.T) {
	root := repoRoot(t)
	goVersion := goToolchain(t, root)
	buildPins := map[string]string{
		"automation/images/ci-go/Dockerfile":        "ARG GO_IMAGE=golang:" + goVersion + "-",
		"automation/images/ci-parity/Dockerfile":    "ARG GO_VERSION=" + goVersion + "\n",
		"docs/site/docs/containerization.md":        "FROM golang:" + goVersion + " AS build",
		"docs/site/docs/termux.md":                  "Go " + goVersion + " is recommended",
		"docs/site/docs/windows.md":                 "Install Git and Go " + goVersion + ".",
		"internal/pigdocs/content/extension-api.md": "Build PiG and Go extensions with Go " + goVersion + ".",
		"internal/pigdocs/content/install.md":       "build it with Go " + goVersion + ".",
		"README.md":                                 "\n- Go " + goVersion + "\n",
		"docs/site/docs/development.md":             "- Go " + goVersion + ";",
		"docs/site/docs/quickstart.md":              "Build the `pig` executable with Go " + goVersion + ".",
		"docs/testing/live-secrets.md":              "Run from the repository root with Go " + goVersion + ".",
		"AGENTS.md":                                 "documented setup commands use Go " + goVersion + ".",
		"automation/dev/toolchains.lock":            "\ngo\t" + goVersion + "\t",
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
	for _, name := range []string{"go.mod", "go.work"} {
		if floor := moduleToolchain(t, root, name); !versionAtMost(t, floor, goVersion) {
			t.Errorf("%s toolchain go%s is newer than the .go-version pin %s", name, floor, goVersion)
		}
	}
	if gomod, gowork := moduleToolchain(t, root, "go.mod"), moduleToolchain(t, root, "go.work"); gomod != gowork {
		t.Errorf("go.work toolchain go%s differs from go.mod toolchain go%s", gowork, gomod)
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
// version from .go-version and forbids any workflow-level Go version literal.
func checkWorkflowsReadGoMod(t *testing.T, root string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`^\s*(?:go-version|GO_VERSION):`)
	versionCheck := regexp.MustCompile(`GOVERSION\)"?\s*=+\s*"?go[0-9]`)
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
				t.Errorf("%s:%d pins Go by literal %q; read .go-version with go-version-file", name, i+1, strings.TrimSpace(line))
			}
			if versionCheck.MatchString(line) {
				t.Errorf("%s:%d compares GOVERSION with a literal %q; read .go-version", name, i+1, strings.TrimSpace(line))
			}
			if strings.Contains(line, "GOVERSION") && strings.Contains(line, "go.mod") {
				t.Errorf("%s:%d compares GOVERSION with go.mod, whose toolchain line is the build floor, not the CI pin %q; read .go-version", name, i+1, strings.TrimSpace(line))
			}
			if versionCheck.MatchString(line) {
				t.Errorf("%s:%d compares GOVERSION with a literal %q; read the go.mod toolchain directive", name, i+1, strings.TrimSpace(line))
			}
			if strings.Contains(line, "GOVERSION") && strings.Contains(line, "go.mod") {
				t.Errorf("%s:%d compares GOVERSION with go.mod, whose toolchain line is the build floor, not the CI pin %q; read .go-version", name, i+1, strings.TrimSpace(line))
			}
			if !setupGo.MatchString(line) {
				continue
			}
			steps++
			if !stepReadsGoMod(lines, i) {
				t.Errorf("%s:%d setup-go step does not set go-version-file: .go-version", name, i+1)
			}
		}
	}
	if steps == 0 {
		t.Fatal("no workflow uses actions/setup-go")
	}
}

// stepReadsGoMod reports whether the workflow step containing lines[index]
// sets go-version-file: .go-version. A step runs from its "- " line to the next
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
		if trimmed == "go-version-file: .go-version" {
			return true
		}
	}
	return false
}
