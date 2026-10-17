package docsdrift

import (
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

func TestDocumentedMakeTargetsExist(t *testing.T) {
	targets := makefileTargets(t)
	checked := 0
	for _, src := range corpus(t) {
		if strings.HasSuffix(src.Rel, "bug-ledger.md") {
			continue
		}
		for i, line := range strings.Split(src.Body, "\n") {
			for _, m := range makeRef.FindAllStringSubmatch(line, -1) {
				checked++
				name := m[1] + m[2]
				if !targets[name] {
					t.Errorf("%s:%d: `make %s` is not a target of the Makefile", src.Rel, i+1, name)
				}
			}
		}
	}
	if checked < 5 {
		t.Fatalf("found only %d `make` references; the pattern is broken", checked)
	}
}

func TestDocumentedGoPackagesExist(t *testing.T) {
	checked := 0
	for _, src := range corpus(t) {
		for i, line := range strings.Split(src.Body, "\n") {
			for _, m := range goRunRef.FindAllStringSubmatch(line, -1) {
				checked++
				dir := filepath.Join(repoRoot, filepath.FromSlash(m[1]))
				if goFiles, _ := filepath.Glob(filepath.Join(dir, "*.go")); len(goFiles) == 0 {
					t.Errorf("%s:%d: `./%s` holds no Go package", src.Rel, i+1, m[1])
				}
			}
		}
	}
	if checked < 3 {
		t.Fatalf("found only %d Go package references; the pattern is broken", checked)
	}
}
