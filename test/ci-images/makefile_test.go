package ciimages

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The targets that run the Pi comparator or the TypeScript inventories reach the installers of their tools through make's own
// dependency graph, so a target that runs them cannot start before the pinned comparator and the locked compiler exist. The
// graph is read from make (-qp), not from the text of the rules, so moving a rule between files or renaming a variable does
// not break it.
func TestComparatorTargetsDependOnTheirInstallers(t *testing.T) {
	prerequisites, _ := makeDatabase(t)
	reaches := func(from, want string) bool {
		seen := map[string]bool{}
		var walk func(string) bool
		walk = func(target string) bool {
			if target == want {
				return true
			}
			if seen[target] {
				return false
			}
			seen[target] = true
			return slices.ContainsFunc(prerequisites[target], walk)
		}
		return walk(from)
	}
	for target, installers := range map[string][]string{
		"test":                      {"parity-deps", "interface-deps"},
		"upstream-mirror":           {"parity-deps"},
		"test-sdk-ts":               {"parity-deps"},
		"parity-bin":                {"parity-deps"},
		"interface-inventory-drift": {"parity-deps", "interface-deps"},
		"parity-deps":               {"interface-deps"},
	} {
		if _, ok := prerequisites[target]; !ok {
			t.Errorf("make has no target %q", target)
			continue
		}
		for _, installer := range installers {
			if !reaches(target, installer) {
				t.Errorf("make %s does not depend, directly or through its prerequisites, on %s", target, installer)
			}
		}
	}
}

// The installers of the comparator's and the inventories' npm packages install them through the content-checked locked
// installer (automation/ci/npm-locked.py): a plain npm install resolves versions and tarballs outside the lockfile check.
func TestInstallersUseTheLockedNpmInstaller(t *testing.T) {
	_, recipes := makeDatabase(t)
	plainNpm := regexp.MustCompile(`\bnpm\s+(?:install|i|ci|add|update)\b`)
	for _, target := range []string{"interface-deps", "parity-deps"} {
		recipe, ok := recipes[target]
		if !ok {
			t.Errorf("make has no recipe for %q", target)
			continue
		}
		if !strings.Contains(recipe, "automation/ci/npm-locked.py") {
			t.Errorf("make %s does not install through automation/ci/npm-locked.py:\n%s", target, recipe)
		}
		if line := plainNpm.FindString(recipe); line != "" {
			t.Errorf("make %s runs %q outside the locked installer:\n%s", target, line, recipe)
		}
	}
}

// makeDatabase reads make's own database (make -qp): each target's prerequisites and its recipe (unexpanded, one line per
// recipe line). Reading the database instead of the rule text keeps the checks valid wherever a rule lives.
func makeDatabase(t *testing.T) (prerequisites map[string][]string, recipes map[string]string) {
	t.Helper()
	out, err := exec.Command("make", "-C", repoRoot(t), "-qp").Output()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 { // -q exits 1 when targets are out of date, which is the normal case
			t.Fatalf("make -qp: %v", err)
		}
	}
	return parseMakeDatabase(string(out))
}

// parseMakeDatabase reads each target's prerequisites and recipe from make's printed database (make -p). A line
// "target: VAR = value" (also := and the other assignment operators) sets a target-specific variable; its words are not
// prerequisites.
func parseMakeDatabase(database string) (prerequisites map[string][]string, recipes map[string]string) {
	prerequisites, recipes = map[string][]string{}, map[string]string{}
	current := ""
	for line := range strings.SplitSeq(database, "\n") {
		if recipe, ok := strings.CutPrefix(line, "\t"); ok && current != "" {
			recipes[current] += recipe + "\n"
			continue
		}
		if line == "" {
			current = ""
			continue
		}
		target, rest, ok := strings.Cut(line, ":")
		if !ok || strings.ContainsAny(target, " \t=%#$") {
			continue
		}
		if strings.Contains(rest, "=") {
			current = ""
			continue
		}
		current = target
		prerequisites[target] = append(prerequisites[target], strings.Fields(rest)...)
	}
	return prerequisites, recipes
}

func TestMakePrerequisitesSkipTargetSpecificVariables(t *testing.T) {
	got, recipes := parseMakeDatabase("# Files\nparity-bin: parity-deps node-runtime\nparity-bin: TOOL := parity-deps\ndrift: LC_ALL := C\ndrift: parity-deps interface-deps\nX := 1\n\t@echo a: b\n")
	if want := []string{"parity-deps", "node-runtime"}; !slices.Equal(got["parity-bin"], want) {
		t.Errorf("parity-bin prerequisites = %q, want %q", got["parity-bin"], want)
	}
	if want := []string{"parity-deps", "interface-deps"}; !slices.Equal(got["drift"], want) {
		t.Errorf("drift prerequisites = %q, want %q", got["drift"], want)
	}
	if len(got) != 2 {
		t.Errorf("targets = %v, want parity-bin and drift", got)
	}
	if want := "@echo a: b\n"; recipes["drift"] != want {
		t.Errorf("drift recipe = %q, want %q", recipes["drift"], want)
	}
}

// readMakeSources returns the root Makefile followed by the fragments it
// includes from automation/make, so assertions hold wherever a rule lives.
func readMakeSources(t *testing.T, root string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "automation", "make", "*.mk"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, path := range append([]string{filepath.Join(root, "Makefile")}, paths...) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}
