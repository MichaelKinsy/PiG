package source

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// The Lisp resolvers are reached only through lisp, which EnableLisp fills, and only EnableLisp refers to them, so the linker drops them from a
// program that never calls it. The lispcell package calls it from its init.
var lisp struct {
	root func(root string) (Definition, error)
	file func(path string, info os.FileInfo) (Definition, error)
}

// EnableLisp makes Resolve classify Lisp extension roots and files.
func EnableLisp() {
	lispFactory = lazyregexp.New(`(?mi)^\s*\(\s*(?:(?:cl:)?defun\s+(?:[^\s():]+:{1,2})?new-extension\s*\(\s*\)|(?:[^\s():]+:{1,2})?define-extension[\s)])`)
	lisp.root = resolveLisp
	lisp.file = resolveLispFile
}

func resolveLispFile(path string, info os.FileInfo) (Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, err
	}
	if lispFactory.Match(data) {
		return Definition{Language: "lisp", Form: Factory, Root: filepath.Dir(path), Package: strings.TrimSuffix(filepath.Base(path), strings.ToLower(filepath.Ext(path))), Factory: "new-extension", Packable: true}, nil
	}
	if executableMode(info) && strings.HasPrefix(string(data), "#!") {
		return Definition{Language: "lisp", Form: Standalone, Root: filepath.Dir(path), Entrypoint: path}, nil
	}
	return Definition{}, fmt.Errorf("Lisp extension %s has no (defun new-extension () ...) factory and is not an executable standalone", path)
}

// lispFactory recognizes the standard Lisp factory: a top-level (defun new-extension () ...), or the declarative
// (pig:define-extension ...) form that expands to exactly that function.
var lispFactory *lazyregexp.Regexp

// lispSourceFiles lists the .lisp files of root; with deep set it also descends into non-hidden subdirectories (at most four levels).
func lispSourceFiles(root string, deep bool) ([]string, error) {
	var files []string
	walkRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(walkRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if entry.IsDir() {
			rel, _ := filepath.Rel(walkRoot, path)
			if path != walkRoot && (!deep || strings.HasPrefix(name, ".") || strings.Count(filepath.ToSlash(rel), "/") >= 4) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(name, ".") && strings.ToLower(filepath.Ext(name)) == ".lisp" {
			files = append(files, filepath.Join(root, strings.TrimPrefix(path, walkRoot)))
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}

func resolveLisp(root string) (Definition, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Definition{}, err
	}
	var systems []string
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && strings.ToLower(filepath.Ext(entry.Name())) == ".asd" {
			systems = append(systems, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		}
	}
	mainPath := filepath.Join(root, "main.lisp")
	hasStandalone := executableScript(mainPath)
	if len(systems) > 1 {
		return Definition{}, fmt.Errorf("Lisp extension %s contains several ASDF systems %v; keep one .asd at the root and declare secondary systems inside it, or select one exact root", root, systems)
	}
	deep := len(systems) == 1
	files, err := lispSourceFiles(root, deep)
	if err != nil {
		return Definition{}, err
	}
	var factories []string
	for _, path := range files {
		if hasStandalone && path == mainPath {
			continue // the standalone's own source may use define-extension
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return Definition{}, err
		}
		if lispFactory.Match(data) {
			factories = append(factories, path)
		}
	}
	if len(factories) > 0 && hasStandalone {
		return Definition{}, fmt.Errorf("Lisp extension %s contains both a new-extension factory and an executable main.lisp standalone; keep one form", root)
	}
	if deep && len(factories) > 0 {
		if len(factories) > 1 {
			return Definition{}, fmt.Errorf("Lisp extension %s defines new-extension in several files %v; keep exactly one factory", root, factories)
		}
		return Definition{Language: "lisp", Form: Factory, Root: root, Package: systems[0], Factory: "new-extension", Packable: true}, nil
	}
	if !deep && len(factories) > 1 {
		return Definition{}, fmt.Errorf("Lisp extension %s contains multiple Lisp factory files %v; select one exact file or use one ASDF system", root, factories)
	}
	if !deep && len(factories) == 1 {
		module := strings.TrimSuffix(filepath.Base(factories[0]), filepath.Ext(factories[0]))
		return Definition{Language: "lisp", Form: Factory, Root: root, Package: module, Factory: "new-extension", Packable: true}, nil
	}
	if deep {
		return Definition{}, fmt.Errorf("Lisp extension %s has an ASDF system but no (defun new-extension () ...) or (pig:define-extension ...) factory in its sources", root)
	}
	if hasStandalone {
		return Definition{Language: "lisp", Form: Standalone, Root: root, Entrypoint: mainPath}, nil
	}
	return Definition{}, fmt.Errorf("Lisp extension %s has no (defun new-extension () ...) factory or executable main.lisp standalone", root)
}
