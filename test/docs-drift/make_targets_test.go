package docsdrift

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A `make <target>` or `go run ./<package>` the docs tell a reader to type must exist. The targets come from the Makefile and the files it includes, and the packages from the tree.

var (
	makeTargetDecl = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9_.%-]*(?: [A-Za-z0-9][A-Za-z0-9_.%-]*)*):(?:[^=]|$)`)
	// A target is named in a code span, or at the start of a line of a code block; "make sure" in prose is not a command.
	makeRef  = regexp.MustCompile("`make ([a-z][a-z0-9-]*)\\b[^`]*`|^\\s*(?:\\$ )?make ([a-z][a-z0-9-]*)\\b")
	goRunRef = regexp.MustCompile(`go (?:run|build|test|vet) (?:-[A-Za-z]+(?:=\S+)? )*\./([A-Za-z0-9_./-]+?)(?:/\.\.\.)?(?:\s|` + "`" + `|$)`)
)

func makefileTargets(t *testing.T) map[string]bool {
	t.Helper()
	targets := map[string]bool{}
	files := []string{"Makefile"}
	mk, _ := filepath.Glob(filepath.Join(repoRoot, "automation", "make", "*.mk"))
	for _, f := range mk {
		rel, _ := filepath.Rel(repoRoot, f)
		files = append(files, filepath.ToSlash(rel))
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range makeTargetDecl.FindAllStringSubmatch(string(data), -1) {
			for name := range strings.FieldsSeq(m[1]) {
				targets[name] = true
			}
		}
	}
	if len(targets) < 100 {
		t.Fatalf("read only %d Makefile targets; the reader is broken", len(targets))
	}
	return targets
}

// missingMakeTargets returns a problem for every `make <target>` a hand-written page names that is not in targets, and the number of references it read.
func missingMakeTargets(sources []docSource, targets map[string]bool) (problems []string, checked int) {
	for _, src := range handWritten(sources) {
		for i, line := range strings.Split(src.Body, "\n") {
			for _, m := range makeRef.FindAllStringSubmatch(line, -1) {
				checked++
				name := m[1] + m[2]
				if !targets[name] {
					problems = append(problems, fmt.Sprintf("%s:%d: `make %s` is not a target of the Makefile", src.Rel, i+1, name))
				}
			}
		}
	}
	return problems, checked
}

// missingGoPackages returns a problem for every `go run ./<package>` (or build, test, vet) a hand-written page names whose directory under root holds no Go file, and the number of references it read.
func missingGoPackages(sources []docSource, root string) (problems []string, checked int) {
	for _, src := range handWritten(sources) {
		for i, line := range strings.Split(src.Body, "\n") {
			for _, m := range goRunRef.FindAllStringSubmatch(line, -1) {
				checked++
				dir := filepath.Join(root, filepath.FromSlash(m[1]))
				if goFiles, _ := filepath.Glob(filepath.Join(dir, "*.go")); len(goFiles) == 0 {
					problems = append(problems, fmt.Sprintf("%s:%d: `./%s` holds no Go package", src.Rel, i+1, m[1]))
				}
			}
		}
	}
	return problems, checked
}

func TestDocumentedMakeTargetsExist(t *testing.T) {
	problems, checked := missingMakeTargets(corpus(t), makefileTargets(t))
	for _, p := range problems {
		t.Error(p)
	}
	if checked < 5 {
		t.Fatalf("found only %d `make` references; the pattern is broken", checked)
	}
}

func TestDocumentedGoPackagesExist(t *testing.T) {
	problems, checked := missingGoPackages(corpus(t), repoRoot)
	for _, p := range problems {
		t.Error(p)
	}
	if checked < 3 {
		t.Fatalf("found only %d Go package references; the pattern is broken", checked)
	}
}

// The bug fix ledger quotes commit messages: the root cause of an entry can be a line that ran `go vet ./...` in another module, or that names a make target that is gone. Neither check reads a generated page as instructions, and both still read the same words on a page a person wrote.
func TestTheCommandChecksSkipOnlyTheGeneratedPages(t *testing.T) {
	const quoted = "- **Root cause:** `GOOS=windows go vet ./...` in apps/p2 stopped at two places, and `make no-such-target` passed.\n"
	sources := []docSource{
		{Rel: "docs/site/docs/bug-ledger.md", Body: quoted},
		{Rel: "internal/pigdocs/content/bug-ledger.md", Body: quoted},
		{Rel: "docs/site/docs/development.md", Body: "Run `go run ./automation/ci/no-such-package`, then `make no-such-target`.\n"},
	}
	problems, checked := missingGoPackages(sources, repoRoot)
	if checked != 1 || len(problems) != 1 || problems[0] != "docs/site/docs/development.md:1: `./automation/ci/no-such-package` holds no Go package" {
		t.Errorf("Go packages: read %d references and reported %q; want the one missing package of the hand-written page", checked, problems)
	}
	problems, checked = missingMakeTargets(sources, map[string]bool{"build": true})
	if checked != 1 || len(problems) != 1 || problems[0] != "docs/site/docs/development.md:1: `make no-such-target` is not a target of the Makefile" {
		t.Errorf("make targets: read %d references and reported %q; want the one missing target of the hand-written page", checked, problems)
	}
	// The skip is the page, not the wording: the ledger's line on a hand-written page is read.
	problems, _ = missingGoPackages([]docSource{{Rel: "docs/site/docs/development.md", Body: quoted}}, repoRoot)
	if len(problems) != 1 || problems[0] != "docs/site/docs/development.md:1: `./...` holds no Go package" {
		t.Errorf("the quoted line on a hand-written page reported %q; want its `./...`", problems)
	}
}
