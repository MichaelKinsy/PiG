// Package source resolves an authorized extension path into one conventional
// factory or exact isolated standalone definition.
package source

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/modfile"
)

// GoSDKModulePath is the Go SDK module path that current Pig scaffolds import.
const GoSDKModulePath = "github.com/MichaelKinsy/PiG/extensions/sdk"

// LegacyGoSDKModulePath is the module path the same Go SDK carried in Pig
// 0.84 and earlier. Factories written against it stay loadable: generated
// runners alias it to the current SDK source, so the extension compiles
// against the SDK that speaks the running host's protocol.
const LegacyGoSDKModulePath = "github.com/mainstai/pig/extensions/sdk"

// IsGoSDKModulePath reports whether path names the Go SDK module under its
// current or legacy module path.
func IsGoSDKModulePath(path string) bool {
	return path == GoSDKModulePath || path == LegacyGoSDKModulePath
}

// Form is one supported author-facing extension execution form.
type Form string

const (
	// Factory is a conventional language factory hosted by a generated runner.
	Factory Form = "factory"
	// Standalone is an exact isolated executable or executable source.
	Standalone Form = "standalone"
)

// Definition is the complete runtime input derived from one authorized source.
type Definition struct {
	Language           string
	Form               Form
	Root               string
	Entrypoint         string
	ModulePath         string
	Package            string
	Factory            string
	Packable           bool
	GoWorkspaceModules []string
	// SDKModulePath is the Go SDK module path a Go factory imports.
	SDKModulePath string
}

// ResolveFunc classifies one exact extension source path. Callers that own a
// bounded resolution snapshot use it to share one source scan across startup
// validation, discovery, trust, and loading.
type ResolveFunc func(string) (Definition, error)

// Resolve classifies one exact source path without reading any extension metadata file.
func Resolve(input string) (Definition, error) {
	absolute, err := filepath.Abs(input)
	if err != nil {
		return Definition{}, fmt.Errorf("resolve extension path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return Definition{}, err
	}
	if !info.IsDir() {
		return resolveFile(absolute, info)
	}
	languages := detectLanguages(absolute)
	if len(languages) == 0 && directoryHasGoSource(absolute) {
		if _, err := findContainingGoModule(absolute); err == nil {
			languages = []string{"go"}
		}
	}
	if len(languages) == 0 {
		return Definition{}, fmt.Errorf("extension %s has no recognized factory or standalone source; use func Extension() *sdk.Extension, pub fn new_extension() -> Extension, def new_extension() -> Extension, a Node default export, or an exact executable", absolute)
	}
	if len(languages) > 1 {
		return Definition{}, fmt.Errorf("extension %s contains multiple extension languages %v; select one exact language root", absolute, languages)
	}
	switch languages[0] {
	case "go":
		return resolveGo(absolute)
	case "rust":
		return resolveRust(absolute)
	case "python":
		return resolvePython(absolute)
	case "node":
		return resolveNode(absolute)
	default:
		return Definition{}, fmt.Errorf("extension %s has unsupported language %q", absolute, languages[0])
	}
}

func resolveFile(path string, info os.FileInfo) (Definition, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".ts" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Definition{}, err
		}
		if executableMode(info) && strings.HasPrefix(string(data), "#!") {
			return Definition{Language: "node", Form: Standalone, Root: filepath.Dir(path), Entrypoint: path}, nil
		}
		if !nodeDefaultExport.Match(data) {
			return Definition{}, fmt.Errorf("Node extension %s has no default extension export; add a Pi-compatible default export or select an exact executable standalone", path)
		}
		return Definition{Language: "node", Form: Factory, Root: filepath.Dir(path), Entrypoint: path}, nil
	}
	if ext == ".py" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Definition{}, err
		}
		if pythonFactory.Match(data) {
			return Definition{Language: "python", Form: Factory, Root: filepath.Dir(path), Package: strings.TrimSuffix(filepath.Base(path), ext), Factory: "new_extension", Packable: true}, nil
		}
		if executableMode(info) && strings.HasPrefix(string(data), "#!") {
			return Definition{Language: "python", Form: Standalone, Root: filepath.Dir(path), Entrypoint: path}, nil
		}
		return Definition{}, fmt.Errorf("Python extension %s has no def new_extension() factory and is not an executable standalone", path)
	}
	if !info.Mode().IsRegular() || !executableMode(info) {
		return Definition{}, fmt.Errorf("extension standalone %s is not an executable regular file", path)
	}
	return Definition{Language: "binary", Form: Standalone, Root: filepath.Dir(path), Entrypoint: path}, nil
}

func detectLanguages(root string) []string {
	var languages []string
	if exists(filepath.Join(root, "go.mod")) || exists(filepath.Join(root, "go.work")) {
		languages = append(languages, "go")
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		languages = append(languages, "rust")
	}
	if exists(filepath.Join(root, "pyproject.toml")) {
		languages = append(languages, "python")
	}
	if exists(filepath.Join(root, "package.json")) {
		languages = append(languages, "node")
	}
	if len(languages) > 0 {
		slices.Sort(languages)
		return languages
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".py":
			if !slices.Contains(languages, "python") {
				languages = append(languages, "python")
			}
		case ".js", ".mjs", ".cjs", ".ts":
			if !slices.Contains(languages, "node") {
				languages = append(languages, "node")
			}
		}
	}
	slices.Sort(languages)
	return languages
}

type goFactory struct {
	modulePath    string
	moduleRoot    string
	packagePath   string
	sdkModulePath string
}

func resolveGo(root string) (Definition, error) {
	moduleRoots, err := goModuleRoots(root)
	if err != nil {
		return Definition{}, err
	}
	var factories []goFactory
	standalone := false
	nonstandard := false
	selectedModule := ""
	for _, moduleRoot := range moduleRoots {
		if root != moduleRoot && pathWithin(moduleRoot, root) {
			selectedModule = moduleRoot
			break
		}
	}
	if selectedModule != "" {
		found, hasMain, hasNonstandard, err := scanGoPackage(selectedModule, root)
		if err != nil {
			return Definition{}, err
		}
		factories, standalone, nonstandard = found, hasMain, hasNonstandard
	} else {
		for _, moduleRoot := range moduleRoots {
			found, hasMain, hasNonstandard, err := scanGoModule(moduleRoot)
			if err != nil {
				return Definition{}, err
			}
			factories = append(factories, found...)
			standalone = standalone || hasMain
			nonstandard = nonstandard || hasNonstandard
		}
	}
	if len(factories) > 0 && standalone {
		return Definition{}, fmt.Errorf("Go extension %s contains both factory and standalone main packages; select one exact root", root)
	}
	if len(factories) > 1 {
		candidates := make([]string, len(factories))
		for i := range factories {
			candidates[i] = factories[i].packagePath
		}
		return Definition{}, fmt.Errorf("Go extension %s contains %d func Extension() *sdk.Extension factories %v; select one exact package root", root, len(candidates), candidates)
	}
	if len(factories) == 1 {
		factory := factories[0]
		return Definition{Language: "go", Form: Factory, Root: factory.moduleRoot, ModulePath: factory.modulePath, Package: factory.packagePath, Factory: "Extension", Packable: true, GoWorkspaceModules: moduleRoots, SDKModulePath: factory.sdkModulePath}, nil
	}
	if standalone {
		if selectedModule != "" {
			return Definition{}, fmt.Errorf("Go standalone %s must select its exact module root %s", root, selectedModule)
		}
		if len(moduleRoots) != 1 {
			return Definition{}, fmt.Errorf("Go standalone %s spans %d modules; select one exact module root", root, len(moduleRoots))
		}
		return Definition{Language: "go", Form: Standalone, Root: moduleRoots[0]}, nil
	}
	if nonstandard {
		return Definition{}, fmt.Errorf("Go extension %s uses a nonstandard factory; expose func Extension() *sdk.Extension or use an executable package main standalone", root)
	}
	return Definition{}, fmt.Errorf("Go extension %s has no func Extension() *sdk.Extension factory or executable package main standalone", root)
}

func goModuleRoots(root string) ([]string, error) {
	if exists(filepath.Join(root, "go.work")) {
		return goWorkspaceModuleRoots(root)
	}
	moduleRoot, err := findContainingGoModule(root)
	if err != nil {
		return nil, err
	}
	for dir := filepath.Dir(moduleRoot); ; dir = filepath.Dir(dir) {
		if exists(filepath.Join(dir, "go.work")) {
			roots, workErr := goWorkspaceModuleRoots(dir)
			if workErr != nil {
				return nil, workErr
			}
			if slices.Contains(roots, moduleRoot) {
				return roots, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return []string{moduleRoot}, nil
}

func goWorkspaceModuleRoots(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil, err
	}
	work, err := modfile.ParseWork(filepath.Join(root, "go.work"), data, nil)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(work.Use))
	for _, use := range work.Use {
		moduleRoot := use.Path
		if !filepath.IsAbs(moduleRoot) {
			moduleRoot = filepath.Join(root, moduleRoot)
		}
		roots = append(roots, filepath.Clean(moduleRoot))
	}
	slices.Sort(roots)
	return roots, nil
}

func findContainingGoModule(root string) (string, error) {
	for dir := root; ; dir = filepath.Dir(dir) {
		if exists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("Go extension %s is not inside a module", root)
		}
	}
}

func directoryHasGoSource(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			return true
		}
	}
	return false
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func scanGoPackage(moduleRoot, packageRoot string) ([]goFactory, bool, bool, error) {
	moduleData, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		return nil, false, false, err
	}
	module, err := modfile.Parse(filepath.Join(moduleRoot, "go.mod"), moduleData, nil)
	if err != nil || module.Module == nil {
		return nil, false, false, fmt.Errorf("parse Go module %s: %w", moduleRoot, err)
	}
	entries, err := os.ReadDir(packageRoot)
	if err != nil {
		return nil, false, false, err
	}
	var factories []goFactory
	hasMain, nonstandard := false, false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(packageRoot, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil, false, false, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		if file.Name.Name == "main" {
			for _, declaration := range file.Decls {
				if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
					hasMain = true
				}
			}
			continue
		}
		aliases := sdkAliases(file)
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Params.NumFields() != 0 {
				continue
			}
			if fn.Name.Name != "Extension" {
				if strings.Contains(strings.ToLower(fn.Name.Name), "extension") {
					nonstandard = true
				}
				continue
			}
			if sdkModulePath, ok := returnsSDKExtension(fn, aliases); ok {
				relative, _ := filepath.Rel(moduleRoot, packageRoot)
				packagePath := module.Module.Mod.Path
				if relative != "." {
					packagePath += "/" + filepath.ToSlash(relative)
				}
				factories = append(factories, goFactory{modulePath: module.Module.Mod.Path, moduleRoot: moduleRoot, packagePath: packagePath, sdkModulePath: sdkModulePath})
			}
		}
	}
	return factories, hasMain, nonstandard, nil
}

func scanGoModule(root string) ([]goFactory, bool, bool, error) {
	moduleData, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, false, false, err
	}
	module, err := modfile.Parse(filepath.Join(root, "go.mod"), moduleData, nil)
	if err != nil || module.Module == nil {
		return nil, false, false, fmt.Errorf("parse Go module %s: %w", root, err)
	}
	modulePath := module.Module.Mod.Path
	var factories []goFactory
	hasMain, nonstandard := false, false
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == "vendor" || entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".") || exists(filepath.Join(path, "go.mod"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if file.Name.Name == "main" {
			for _, declaration := range file.Decls {
				if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
					hasMain = true
				}
			}
			return nil
		}
		aliases := sdkAliases(file)
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Params.NumFields() != 0 {
				continue
			}
			if fn.Name.Name != "Extension" {
				if strings.Contains(strings.ToLower(fn.Name.Name), "extension") {
					nonstandard = true
				}
				continue
			}
			if sdkModulePath, ok := returnsSDKExtension(fn, aliases); ok {
				relative, _ := filepath.Rel(root, filepath.Dir(path))
				packagePath := modulePath
				if relative != "." {
					packagePath += "/" + filepath.ToSlash(relative)
				}
				factories = append(factories, goFactory{modulePath: modulePath, moduleRoot: root, packagePath: packagePath, sdkModulePath: sdkModulePath})
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, false, err
	}
	slices.SortFunc(factories, func(a, b goFactory) int { return strings.Compare(a.packagePath, b.packagePath) })
	return factories, hasMain, nonstandard, nil
}

// sdkAliases maps each file-local name bound to a Go SDK import to the SDK
// module path it imports.
func sdkAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		if !IsGoSDKModulePath(importPath) {
			continue
		}
		name := "sdk"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = importPath
	}
	return aliases
}

// returnsSDKExtension reports whether fn returns *sdk.Extension through one of
// the file's SDK aliases, and which SDK module path that alias imports.
func returnsSDKExtension(fn *ast.FuncDecl, aliases map[string]string) (string, bool) {
	if fn.Type.Results == nil || fn.Type.Results.NumFields() != 1 {
		return "", false
	}
	star, ok := fn.Type.Results.List[0].Type.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	selector, ok := star.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Extension" {
		return "", false
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	sdkModulePath, ok := aliases[alias.Name]
	return sdkModulePath, ok
}

var rustFactory = regexp.MustCompile(`(?m)^\s*pub\s+fn\s+new_extension\s*\(\s*\)\s*->\s*(?:pig_sdk::)?Extension\b`)

func resolveRust(root string) (Definition, error) {
	var manifest struct {
		Package struct {
			Name string `toml:"name"`
		} `toml:"package"`
		Lib struct {
			Name string `toml:"name"`
		} `toml:"lib"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "Cargo.toml"), &manifest); err != nil {
		return Definition{}, fmt.Errorf("parse Rust manifest: %w", err)
	}
	libPath := filepath.Join(root, "src", "lib.rs")
	mainPath := filepath.Join(root, "src", "main.rs")
	hasLib, hasMain := exists(libPath), exists(mainPath)
	if hasLib && hasMain {
		return Definition{}, fmt.Errorf("Rust extension %s contains both factory and standalone targets; keep src/lib.rs with pub fn new_extension() -> Extension, or keep only src/main.rs for standalone", root)
	}
	if hasLib {
		data, err := os.ReadFile(libPath)
		if err != nil {
			return Definition{}, err
		}
		if !rustFactory.Match(data) {
			return Definition{}, fmt.Errorf("Rust extension %s has no pub fn new_extension() -> Extension factory; use that standard factory or a src/main.rs standalone", root)
		}
		packageName := manifest.Package.Name
		if packageName == "" {
			return Definition{}, fmt.Errorf("Rust extension %s has no Cargo package name", root)
		}
		crate := strings.ReplaceAll(packageName, "-", "_")
		if manifest.Lib.Name != "" && manifest.Lib.Name != crate {
			return Definition{}, fmt.Errorf("Rust extension %s uses nonstandard lib name %q; use the Cargo package-derived crate %q with pub fn new_extension() or standalone form", root, manifest.Lib.Name, crate)
		}
		return Definition{Language: "rust", Form: Factory, Root: root, Package: packageName, Factory: "new_extension", Packable: true}, nil
	}
	if hasMain {
		return Definition{Language: "rust", Form: Standalone, Root: root}, nil
	}
	return Definition{}, fmt.Errorf("Rust extension %s has no src/lib.rs pub fn new_extension() factory or src/main.rs standalone", root)
}

var pythonFactory = regexp.MustCompile(`(?m)^def[ \t]+new_extension[ \t]*\([ \t]*\)[ \t]*->[ \t]*(?:pig_sdk\.)?Extension[ \t]*:`)

func resolvePython(root string) (Definition, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Definition{}, err
	}
	var factories []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || strings.ToLower(filepath.Ext(entry.Name())) != ".py" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return Definition{}, err
		}
		if pythonFactory.Match(data) {
			factories = append(factories, path)
		}
	}
	if len(factories) > 1 {
		return Definition{}, fmt.Errorf("Python extension %s contains multiple Python factory modules %v; select one exact module", root, factories)
	}
	mainPath := filepath.Join(root, "main.py")
	hasStandalone := executableScript(mainPath)
	if len(factories) == 1 && hasStandalone {
		return Definition{}, fmt.Errorf("Python extension %s contains both a new_extension factory and executable main.py standalone; keep one form", root)
	}
	if len(factories) == 1 {
		module := strings.TrimSuffix(filepath.Base(factories[0]), filepath.Ext(factories[0]))
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(module) {
			return Definition{}, fmt.Errorf("Python extension module %q is not importable; use letters, digits, and underscores, and do not start with a digit", module)
		}
		return Definition{Language: "python", Form: Factory, Root: root, Package: module, Factory: "new_extension", Packable: true}, nil
	}
	if hasStandalone {
		return Definition{Language: "python", Form: Standalone, Root: root, Entrypoint: mainPath}, nil
	}
	return Definition{}, fmt.Errorf("Python extension %s has no def new_extension() factory or executable main.py standalone", root)
}

var nodeDefaultExport = regexp.MustCompile(`(?m)\bexport\s+default\b`)

func resolveNode(root string) (Definition, error) {
	entries, err := nodeEntrypoints(root)
	if err != nil {
		return Definition{}, err
	}
	if len(entries) != 1 {
		return Definition{}, fmt.Errorf("Node extension %s resolves to %d entrypoints; select one exact Pi-compatible default export", root, len(entries))
	}
	data, err := os.ReadFile(entries[0])
	if err != nil {
		return Definition{}, err
	}
	if !nodeDefaultExport.Match(data) {
		return Definition{}, fmt.Errorf("Node extension %s has no Pi-compatible default export; export one default extension function or select an exact executable standalone", entries[0])
	}
	return Definition{Language: "node", Form: Factory, Root: root, Entrypoint: entries[0]}, nil
}

func nodeEntrypoints(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err == nil {
		var manifest struct {
			PI struct {
				Extensions []string `json:"extensions"`
			} `json:"pi"`
		}
		if json.Unmarshal(data, &manifest) == nil && len(manifest.PI.Extensions) > 0 {
			entries := make([]string, 0, len(manifest.PI.Extensions))
			for _, relative := range manifest.PI.Extensions {
				path := filepath.Join(root, filepath.FromSlash(relative))
				if !exists(path) {
					return nil, fmt.Errorf("Node extension %s declares missing pi.extensions entry %q", root, relative)
				}
				entries = append(entries, path)
			}
			return entries, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var entries []string
	for _, name := range []string{"index.ts", "index.js", "main.ts", "main.js", "extension.ts", "extension.js", "index.mjs", "main.mjs", "extension.mjs"} {
		if path := filepath.Join(root, name); exists(path) {
			entries = append(entries, path)
		}
	}
	return entries, nil
}

func executableScript(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || !executableMode(info) {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && strings.HasPrefix(string(data), "#!")
}

// executableMode reports whether info grants execute permission. Windows file
// modes carry no execute bits, so there a standalone is identified by its
// shebang (scripts) or its selection (binaries) alone.
func executableMode(info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode()&0o111 != 0
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
