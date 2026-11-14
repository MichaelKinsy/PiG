package extensionconformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The guard of docs/parity/divergences/D645.md: a name a caller supplies is looked up as an own entry, never through JavaScript's Object.prototype.
// Two things brought the class into PiG before: a Go table of the Object.prototype member names, written to answer as Pi's plain objects do, and a
// plain-object lookup in JavaScript PiG owns. The first table below lists the Go files that may hold such a name table, each with its reason; the
// second lists the lookups that were cured in PiG's own JavaScript, in the form they had.

// objectPrototypeTableNames are the names a file is searched for; three of them in one production file make a name table.
var objectPrototypeTableNames = []string{"constructor", "toString", "__proto__", "hasOwnProperty", "valueOf", "isPrototypeOf", "toLocaleString", "propertyIsEnumerable", "__defineGetter__"}

// objectPrototypeTableFiles are the production Go files that hold three or more of the names as string literals, and why each may.
var objectPrototypeTableFiles = map[string]string{
	"internal/typebox/engine/value.go":           "TypeBox's own `key in value` rules for unsafe and inherited keys, ported with the library",
	"durable/durabletest/storage_conformance.go": "a storage conformance suite that stores documents under these names to prove they are plain keys",
}

// curedJavaScriptLookups are the plain-object lookups by a supplied name that PiG's own JavaScript had, per file: none may come back.
var curedJavaScriptLookups = map[string][]string{
	"coding/extension/host/subprocess/runtime-node/runtime.mjs": {
		`this\.(foregrounds|backgrounds)\[(token|color)\]`,
		`statuses\[key\] = `,
		`providers\?\.\[`,
		`providers\[name\]`,
		`this\.state\.settings = snapshot\.settings`,
	},
}

func TestNoLookupAnswersForObjectPrototypeMemberNames(t *testing.T) {
	root := findModuleRoot(t)
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "tmp" || name == "testdata" || name == "third_party" || name == "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		count := 0
		for _, member := range objectPrototypeTableNames {
			if strings.Contains(string(data), `"`+member+`"`) {
				count++
			}
		}
		if count >= 3 {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			found[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file := range found {
		if _, ok := objectPrototypeTableFiles[file]; !ok {
			t.Errorf("%s holds three or more Object.prototype member names as string literals: a table that makes a Go lookup answer for constructor, toString or __proto__ as a plain JavaScript object does. Look the name up as an own entry (D645), or list the file in objectPrototypeTableFiles with its reason", file)
		}
	}
	listed := make([]string, 0, len(objectPrototypeTableFiles))
	for file := range objectPrototypeTableFiles {
		listed = append(listed, file)
	}
	slices.Sort(listed)
	for _, file := range listed {
		if !found[file] {
			t.Errorf("%s is listed in objectPrototypeTableFiles and holds no such name table any more: take it off the list", file)
		}
	}
	for file, patterns := range curedJavaScriptLookups {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range patterns {
			if at := regexp.MustCompile(pattern).FindIndex(data); at != nil {
				line := 1 + strings.Count(string(data[:at[0]]), "\n")
				t.Errorf("%s:%d has the plain-object lookup %s back: a name that is a member of Object.prototype finds the inherited member there, and `__proto__` sets a prototype. Use ownEntry and setOwnEntry (D645)", file, line, pattern)
			}
		}
	}
}
