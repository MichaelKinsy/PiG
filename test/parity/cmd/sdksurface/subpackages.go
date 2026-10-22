package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// An SDK's public subpackages (Go extensions/sdk/tui, Rust sdk-rs/src/tui, Python pig_sdk/tui) hold API a
// package-level SDK scan does not see. Their symbols are added to the SDK's set qualified by the package name,
// the way kit views are: tui.VisibleWidth in Go and Python, tui::visible_width in Rust. A ledger cell names
// them with that qualifier (native:tui.VisibleWidth).
//
// Go and Python: every exported symbol of the package. Rust: only what the module re-exports with `pub use`
// (or declares `pub` itself in mod.rs), and only items with plain `pub`: a pub(crate) item is not API.

// sdkModulePath is the module whose internal packages a subpackage alias may point into.
const sdkModulePath = "github.com/MichaelKinsy/PiG"

// goSubpackageSkip are directories of extensions/sdk that are not importable SDK API.
var goSubpackageSkip = map[string]bool{"internal": true, "testdata": true, "upgrade": true, "kit": true}

func subdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func qualify(to, from symbolSet, prefix string) {
	for sym := range from {
		to[prefix+sym] = true
	}
}

// addSubpackageSymbols adds each SDK's public subpackages (other than the component kit, which addKitSymbols
// qualifies as kit) to its symbols.
func addSubpackageSymbols(root string, goSyms, rs, py symbolSet) error {
	goRoot := filepath.Join(root, "extensions/sdk")
	dirs, err := subdirs(goRoot)
	if err != nil {
		return err
	}
	for _, name := range dirs {
		if goSubpackageSkip[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		pkg, err := parseGoPackage(filepath.Join(goRoot, name))
		if err != nil || len(pkg.symbols) == 0 {
			continue // a directory with no Go files is not a package
		}
		qualify(goSyms, pkg.symbols, name+".")
		// A type alias of a type another package of this module declares (type X = internal.Y) has that type's methods and
		// fields: they are the subpackage's API under the alias's name.
		for _, a := range pkg.aliases {
			rel, ok := strings.CutPrefix(a.importPath, sdkModulePath+"/")
			if !ok {
				continue
			}
			target, err := parseGoPackage(filepath.Join(root, rel))
			if err != nil {
				return fmt.Errorf("go %s: alias %s: %w", name, a.name, err)
			}
			for sym := range target.symbols {
				if rest, ok := strings.CutPrefix(sym, a.target+"."); ok {
					goSyms[name+"."+a.name+"."+rest] = true
				}
			}
		}
	}

	rsRoot := filepath.Join(root, "extensions/sdk-rs/src")
	if dirs, err = subdirs(rsRoot); err != nil {
		return err
	}
	for _, name := range dirs {
		if name == "kit" || !fileExists(filepath.Join(rsRoot, name, "mod.rs")) {
			continue
		}
		syms, err := parseRustModule(filepath.Join(rsRoot, name))
		if err != nil {
			return fmt.Errorf("rust %s: %w", name, err)
		}
		qualify(rs, syms, name+"::")
	}

	pyRoot := filepath.Join(root, "extensions/sdk-py/pig_sdk")
	if dirs, err = subdirs(pyRoot); err != nil {
		return err
	}
	for _, name := range dirs {
		if !fileExists(filepath.Join(pyRoot, name, "__init__.py")) || strings.HasPrefix(name, "_") || name == "__pycache__" {
			continue
		}
		syms, err := parsePythonPackage(filepath.Join(pyRoot, name))
		if err != nil {
			return fmt.Errorf("python %s: %w", name, err)
		}
		qualify(py, syms, name+".")
	}
	return nil
}

var (
	rsUseRE    = regexp.MustCompile(`pub use ([^;]*);`)
	rsPubModRE = regexp.MustCompile(`(?m)^\s*pub mod ([A-Za-z_][A-Za-z0-9_]*)\s*;`)
)

// parseRustModule reads a Rust module directory (mod.rs and its files) and keeps the symbols the module makes
// public: the names mod.rs re-exports (`pub use file::{a, B}`, `pub use file::*`, `pub use file::name`), each
// with its members, and the plain-pub items mod.rs declares itself.
func parseRustModule(dir string) (symbolSet, error) {
	modRaw, err := os.ReadFile(filepath.Join(dir, "mod.rs"))
	if err != nil {
		return nil, err
	}
	modCode := scanCode(string(modRaw), "rust")
	perFile := map[string]symbolSet{} // module name -> its plain-pub symbols
	files, err := sourceFiles(dir, ".rs", nil)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".rs")
		if base == "mod" || base == "tests" || strings.HasSuffix(base, "_tests") {
			continue
		}
		syms, err := parseRustItems(dir, rsPublicItemRE, func(name string) bool { return name == filepath.Base(f) })
		if err != nil {
			return nil, err
		}
		perFile[base] = syms
	}
	out := symbolSet{}
	own, err := parseRustItems(dir, rsPublicItemRE, func(name string) bool { return name == "mod.rs" })
	if err != nil {
		return nil, err
	}
	for s := range own {
		out[s] = true
	}
	take := func(syms symbolSet, name string) {
		for s := range syms {
			if s == name || strings.HasPrefix(s, name+"::") || strings.HasPrefix(s, name+".") {
				out[s] = true
			}
		}
	}
	for _, m := range rsUseRE.FindAllStringSubmatch(modCode, -1) {
		path := strings.Join(strings.Fields(m[1]), " ")
		module, rest, ok := strings.Cut(path, "::")
		if !ok {
			continue
		}
		syms := perFile[strings.TrimSpace(module)]
		if syms == nil {
			continue
		}
		rest = strings.TrimSpace(rest)
		switch {
		case rest == "*":
			for s := range syms {
				out[s] = true
			}
		case strings.HasPrefix(rest, "{"):
			for item := range strings.SplitSeq(strings.Trim(rest, "{} "), ",") {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				if before, _, aliased := strings.Cut(item, " as "); aliased {
					item = strings.TrimSpace(before)
				}
				take(syms, item)
			}
		default:
			if before, _, aliased := strings.Cut(rest, " as "); aliased {
				rest = strings.TrimSpace(before)
			}
			take(syms, rest)
		}
	}
	for _, m := range rsPubModRE.FindAllStringSubmatch(modCode, -1) {
		for s := range perFile[m[1]] {
			out[m[1]+"::"+s] = true
		}
	}
	return out, nil
}

var pyReexportRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*) as ([A-Za-z_][A-Za-z0-9_]*)\b`)

// parsePythonPackage reads a Python package directory and keeps the symbols its __init__.py makes public: the
// names it defines and the names it re-exports with `from ._x import name as name` (or lists in __all__), each
// with its members.
func parsePythonPackage(dir string) (symbolSet, error) {
	all, err := parsePythonFiles(mustGlob(filepath.Join(dir, "*.py")))
	if err != nil {
		return nil, err
	}
	initSyms, err := parsePythonFiles([]string{filepath.Join(dir, "__init__.py")})
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		return nil, err
	}
	exported := map[string]bool{}
	for _, m := range pyReexportRE.FindAllStringSubmatch(string(raw), -1) {
		if m[1] == m[2] {
			exported[m[2]] = true
		}
	}
	for s := range initSyms {
		root, _, _ := strings.Cut(s, ".")
		exported[root] = true
	}
	out := symbolSet{}
	for s := range all {
		root, _, _ := strings.Cut(s, ".")
		if exported[root] && !strings.HasPrefix(root, "_") {
			out[s] = true
		}
	}
	return out, nil
}

func mustGlob(pattern string) []string {
	files, _ := filepath.Glob(pattern)
	sort.Strings(files)
	return files
}
