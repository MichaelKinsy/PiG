package chord

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Ports packages/chord/test/boundary.test.ts: the package does not depend on Pi packages or files outside Chord. The Go form scans the import declarations of every non-test source file under internal/chord. The PiG imports allowed from outside are internal/text, the UTF-16 helper that orders member names as JavaScript sorts them, and chord/delta, Chord's own overlay tracker.
func TestPackageBoundaryDoesNotDependOnPiPackagesOrFilesOutsideChord(t *testing.T) {
	const module = "github.com/MichaelKinsy/PiG/"
	allowed := map[string]bool{module + "internal/text": true, module + "chord/delta": true}
	var violations []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(imported, module) && !strings.HasPrefix(imported, module+"internal/chord") && !allowed[imported] {
				violations = append(violations, path+": "+imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("imports outside chord: %v", violations)
	}
}
