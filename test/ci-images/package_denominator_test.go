package ciimages

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The guards in this file take their denominator from the tree (go list, the go.mod files on disk), never from a hand list: a package or module that a new directory adds is checked the moment it exists.

// packagesWithTests lists the packages of the root module that have a _test.go file when no build tag is set.
func packagesWithTests(t *testing.T, tags string) (all, tested []string, module string) {
	t.Helper()
	args := []string{"list"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "-f", "{{.Module.Path}} {{.ImportPath}} {{len .TestGoFiles}} {{len .XTestGoFiles}}", "./...")
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			t.Fatalf("unexpected go list line %q", line)
		}
		module = f[0]
		all = append(all, f[1])
		if f[2] != "0" || f[3] != "0" {
			tested = append(tested, f[1])
		}
	}
	return all, tested, module
}

// groupedReport runs `test-grouped.sh report` in the repository and returns the packages of each group. The sharded list names ./relative packages with a :count; the subprocess and conformance packages appear both as a bounded group and as shard specs, one gate (the heavy one) that runs them whole or in shards.
func groupedReport(t *testing.T, module string) map[string][]string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), testenv.Bash(t), repoScript(t, "automation/ci/test-grouped.sh"), "report")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("test-grouped.sh report: %v", err)
	}
	groups := map[string][]string{}
	var group string
	for line := range strings.SplitSeq(string(out), "\n") {
		if name, ok := strings.CutPrefix(line, "[test-grouped] "); ok {
			if g, ok := strings.CutSuffix(name, " packages:"); ok {
				group = g
				if group == "subprocess" || group == "sharded" {
					group = "heavy"
				}
			} else {
				group = ""
			}
			continue
		}
		pkg, ok := strings.CutPrefix(line, "  ")
		if !ok || group == "" || strings.HasPrefix(pkg, "(none)") {
			continue
		}
		pkg = strings.Fields(pkg)[0]
		if rel, ok := strings.CutPrefix(pkg, "./"); ok {
			pkg = module + "/" + path.Clean(strings.SplitN(rel, ":", 2)[0])
		}
		if !slices.Contains(groups[group], pkg) {
			groups[group] = append(groups[group], pkg)
		}
	}
	// In report mode test-grouped.sh leaves the sharded packages in the fast list; every other mode takes them out (the resolve step guarded by MODE != report), so they run in their shards only.
	groups["fast"] = slices.DeleteFunc(groups["fast"], func(pkg string) bool { return slices.Contains(groups["heavy"], pkg) })
	return groups
}

func TestEveryTestedPackageIsInExactlyOneGroupedRun(t *testing.T) {
	all, tested, module := packagesWithTests(t, "")
	groups := groupedReport(t, module)
	if len(tested) == 0 || len(groups["fast"]) == 0 {
		t.Fatalf("no packages found: go list has %d tested, report has %v", len(tested), groups)
	}
	member := map[string][]string{}
	for group, pkgs := range groups {
		for _, pkg := range pkgs {
			member[pkg] = append(member[pkg], group)
			if !slices.Contains(all, pkg) {
				t.Errorf("test-grouped.sh report lists %s in %q, which is not a package of go list ./...: drop it from that list in automation/ci/test-grouped.sh", pkg, group)
			}
		}
	}
	for _, pkg := range tested {
		switch groups := member[pkg]; len(groups) {
		case 0:
			t.Errorf("package %s has tests but is in no group of test-grouped.sh report: it must join FAST_PKGS (the default), HEAVY_PKGS, SERIAL_PKGS or SHARDED_SPEC in automation/ci/test-grouped.sh", pkg)
		case 1:
		default:
			t.Errorf("package %s has tests and is in several groups %v of test-grouped.sh report: it must be in exactly one", pkg, groups)
		}
	}
	for group, pkgs := range groups {
		t.Logf("group %s: %d packages", group, len(pkgs))
	}
	t.Logf("go list ./...: %d packages, %d with tests", len(all), len(tested))
}

// nestedModules returns the directories (relative, slash form) of every go.mod below the root that a maintainer keeps: not under node_modules, .upstream, third_party, tmp or a testdata directory.
func nestedModules(t *testing.T, root string) []string {
	t.Helper()
	var modules []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".upstream", "third_party", "testdata", ".git":
				return filepath.SkipDir
			}
			if p == filepath.Join(root, "tmp") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" && filepath.Dir(p) != root {
			rel, err := filepath.Rel(root, filepath.Dir(p))
			if err != nil {
				return err
			}
			modules = append(modules, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(modules)
	return modules
}

// moduleFiles counts the Go files and _test.go files that belong to the module at dir itself: files inside a deeper nested go.mod are the deeper module's.
func moduleFiles(t *testing.T, root, dir string) (goFiles, testFiles int) {
	t.Helper()
	top := filepath.Join(root, filepath.FromSlash(dir))
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if p != top {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			goFiles++
			if strings.HasSuffix(d.Name(), "_test.go") {
				testFiles++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return goFiles, testFiles
}

var goNestedModulesDecl = regexp.MustCompile(`(?m)^GO_NESTED_MODULES\s*:?=((?:[^\n]*\\\n)*[^\n]*)`)

func TestEveryNestedModuleWithTestsIsInGoNestedModules(t *testing.T) {
	root := repoRoot(t)
	match := goNestedModulesDecl.FindStringSubmatch(readMakeSources(t, root))
	if match == nil {
		t.Fatal("no GO_NESTED_MODULES declaration in the Makefile")
	}
	listed := strings.Fields(strings.ReplaceAll(match[1], "\\\n", " "))
	if len(listed) == 0 {
		t.Fatal("GO_NESTED_MODULES is empty")
	}
	var withTests, withoutTests []string
	for _, dir := range nestedModules(t, root) {
		goFiles, testFiles := moduleFiles(t, root, dir)
		switch {
		case testFiles > 0:
			withTests = append(withTests, dir)
			if !slices.Contains(listed, dir) {
				t.Errorf("nested module %s has %d _test.go files but is not in GO_NESTED_MODULES in the Makefile: add it so test-go-modules and test-sdk-go run it", dir, testFiles)
			}
		case goFiles > 0:
			withoutTests = append(withoutTests, dir)
		}
	}
	for _, dir := range listed {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), "go.mod")); err != nil {
			t.Errorf("GO_NESTED_MODULES lists %s, which has no go.mod", dir)
		}
	}
	t.Logf("nested modules with tests (%d): %v", len(withTests), withTests)
	t.Logf("nested modules with Go files and no _test.go (information only, %d): %v", len(withoutTests), withoutTests)
}

// taggedOnlyTargets names, for each package whose tests build only under a tag, the make targets that run it. A package that go list -tags reveals and is absent here fails the test: add it with the target that runs it.
var taggedOnlyTargets = map[string][]string{
	"test/journeys":      {"journeys", "journeys-changed"},
	"test/parity/runner": {"parity-live-owner", "ci-node-runtime"},
}

func TestEveryTaggedOnlyPackageHasAMakeTarget(t *testing.T) {
	_, plain, module := packagesWithTests(t, "")
	_, tagged, _ := packagesWithTests(t, "parity,integration,pigfault,live")
	makefile := readMakeSources(t, repoRoot(t))
	var found []string
	for _, pkg := range tagged {
		if slices.Contains(plain, pkg) {
			continue
		}
		rel := strings.TrimPrefix(pkg, module+"/")
		found = append(found, rel)
		targets, ok := taggedOnlyTargets[rel]
		if !ok {
			t.Errorf("package %s has tests only under a build tag and is not in taggedOnlyTargets: name the make target that runs it", pkg)
			continue
		}
		for _, target := range targets {
			if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `[^\n]*:`).MatchString(makefile) {
				t.Errorf("package %s: make target %q is not defined in the Makefile or automation/make/*.mk", pkg, target)
			}
		}
		if !strings.Contains(makefile, "-tags parity ./"+rel) && !strings.Contains(makefile, "-tags=parity -count=1 ./"+rel) &&
			!strings.Contains(makefile, "./"+rel+" ") {
			t.Errorf("package %s: no go test line in the make sources runs ./%s", pkg, rel)
		}
	}
	for rel := range taggedOnlyTargets {
		if !slices.Contains(found, rel) {
			t.Errorf("taggedOnlyTargets names %s, which is no longer a tagged-only tested package: drop it", rel)
		}
	}
	t.Logf("tagged-only packages: %v", found)
}
