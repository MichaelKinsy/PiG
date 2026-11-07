package boltruntime

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// A compiled-in extension runs from one executable: its files are not on disk beside it, so code that reads a file by its own location finds nothing where Node and Bun
// find it. The build therefore compiles such a file from a copy in which each expression that names its location says what it is in the installed file (rewrite.go).
// scanPathSensitive makes those rewrites and finds what they cannot make, so that the build refuses it instead of changing the extension's behavior.

// pathFinding is a file that locates itself in a way the rewrite cannot pin, and uses the file system.
type pathFinding struct {
	File   string
	Marker string
}

func (f pathFinding) String() string {
	return fmt.Sprintf("%s locates itself by %s, which cannot be pinned to the installed file, and reads files", f.File, f.Marker)
}

// rewritePlan is what scanning a set's files found.
type rewritePlan struct {
	// Rewrites maps each file that names its location to the source the build is to read in its place.
	Rewrites map[string]string
	// Finding is the first file that names its location in a way the rewrite cannot pin and that reads files, or nil.
	Finding *pathFinding
}

// isScript is whether the compiler reads a file as a script: JavaScript or TypeScript, less type declarations, which hold no values.
func isScript(path string) bool {
	base := filepath.Base(path)
	for _, decl := range []string{".d.ts", ".d.mts", ".d.cts"} {
		if strings.HasSuffix(base, decl) {
			return false
		}
	}
	switch filepath.Ext(base) {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx":
		return true
	}
	return false
}

// notUTF8Markers are what a source that cannot be rewritten has to hold for its location to matter.
var notUTF8Markers = [][]byte{[]byte("import.meta"), []byte("__dirname"), []byte("__filename")}

// scanPathSensitive rewrites each script among files that names its own location (rewriteModule) and returns the changed sources. It refuses a file that names its
// location in a way the rewrite cannot pin to the installed file and that reads files or starts processes: `import.meta` used as a value (`const m = import.meta`,
// `import.meta["url"]`, `import.meta?.url`, `{ ...import.meta }`), a path that is not UTF-8, and a source that is not UTF-8 and mentions a location. A file that names
// its location and reads nothing is left as it is: nothing observable depends on it.
func scanPathSensitive(files []string) (rewritePlan, error) {
	plan := rewritePlan{Rewrites: map[string]string{}}
	for _, file := range files {
		if !isScript(file) {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return rewritePlan{}, err
		}
		if !utf8.Valid(data) {
			for _, marker := range notUTF8Markers {
				if bytes.Contains(data, marker) && plan.Finding == nil {
					plan.Finding = &pathFinding{File: file, Marker: "a source that is not UTF-8"}
				}
			}
			continue
		}
		result := rewriteModule(string(data), file)
		if result.Unpinned != "" && result.ReadsFiles && plan.Finding == nil {
			plan.Finding = &pathFinding{File: file, Marker: result.Unpinned}
		}
		if result.Changed {
			plan.Rewrites[file] = result.Source
		}
	}
	return plan, nil
}
