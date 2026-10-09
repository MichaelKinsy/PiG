package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A documented rename names a Go declaration; when a type is renamed the entry must follow, or the rows it binds silently
// fall back to "no field or method" (the BashExecutionBlock -> BashExecutionComponent drift left 36 rows open).
func TestRenameTargetsNameExistingDeclarations(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	renames := renameTable{}
	if err := readJSON(filepath.Join(root, renamesFile), &renames); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for id, target := range renames {
		file, symbol, ok := strings.Cut(target, "#")
		if !ok {
			t.Errorf("%s: target %q has no #symbol", id, target)
			continue
		}
		src, cached := sources[file]
		if !cached {
			data, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				t.Errorf("%s: target file %s: %v", id, file, err)
				continue
			}
			src = string(data)
			sources[file] = src
		}
		owner, member, hasMember := strings.Cut(symbol, ".")
		var declared bool
		if hasMember {
			// The method lives in the named file; the owner type and a field may live anywhere in the package.
			method := regexp.MustCompile(`func \(\w+ \*?` + regexp.QuoteMeta(owner) + `(\[[^\]]*\])?\) ` + regexp.QuoteMeta(member) + `\b`).MatchString(src)
			declared = method || packageDeclares(t, root, file, `(?m)^\s+`+regexp.QuoteMeta(member)+`\b`)
		} else {
			declared = regexp.MustCompile(`(?m)^(?:func|type|var|const)\s+` + regexp.QuoteMeta(owner) + `\b|^\s+` + regexp.QuoteMeta(owner) + `\b`).MatchString(src)
		}
		if !declared {
			t.Errorf("%s: %s does not name a declaration in %s", id, target, file)
		}
	}
}

func packageDeclares(t *testing.T, root, file, pattern string) bool {
	t.Helper()
	re := regexp.MustCompile(pattern)
	entries, err := os.ReadDir(filepath.Join(root, filepath.Dir(file)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.Dir(file), entry.Name()))
		if err == nil && re.Match(data) {
			return true
		}
	}
	return false
}
