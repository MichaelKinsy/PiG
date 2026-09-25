package pigletbuild

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

const fusedProcessHazardDiagnostic = "PIGLET_FUSED_PROCESS_HAZARD"

type fusedModule struct {
	path string
	root string
}

type fusedHazard struct {
	extension string
	position  token.Position
	symbol    string
}

func (h fusedHazard) message() string {
	return fmt.Sprintf("%s: fused extension %q: %s uses %s; fused extensions share the Pig process; return errors and use extension host/UI APIs instead", fusedProcessHazardDiagnostic, h.extension, h.position, h.symbol)
}

func vetFusedPackages(ctx context.Context, fused []fusedEntry) error {
	var hazards []fusedHazard
	for _, entry := range fused {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryHazards, err := inspectFusedEntry(ctx, entry)
		if err != nil {
			return fmt.Errorf("vet fused extension %q: %w", entry.Name, err)
		}
		hazards = append(hazards, entryHazards...)
	}
	if len(hazards) == 0 {
		return nil
	}
	slices.SortFunc(hazards, func(a, b fusedHazard) int {
		return strings.Compare(a.extension+"\x00"+a.position.String()+"\x00"+a.symbol, b.extension+"\x00"+b.position.String()+"\x00"+b.symbol)
	})
	messages := make([]string, len(hazards))
	for i, hazard := range hazards {
		messages[i] = hazard.message()
	}
	return fmt.Errorf("fused package vet failed:\n%s", strings.Join(messages, "\n"))
}

func inspectFusedEntry(ctx context.Context, entry fusedEntry) ([]fusedHazard, error) {
	modules, err := fusedModules(entry)
	if err != nil {
		return nil, err
	}
	pending := []string{entry.Package}
	visited := make(map[string]struct{})
	var hazards []fusedHazard
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		importPath := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, ok := visited[importPath]; ok {
			continue
		}
		visited[importPath] = struct{}{}
		dir, ok := fusedPackageDir(importPath, modules)
		if !ok {
			continue
		}
		packageHazards, imports, err := inspectFusedPackage(entry.Name, dir)
		if err != nil {
			return nil, err
		}
		hazards = append(hazards, packageHazards...)
		for _, dependency := range imports {
			if _, local := fusedPackageDir(dependency, modules); local {
				pending = append(pending, dependency)
			}
		}
	}
	return hazards, nil
}

func fusedModules(entry fusedEntry) ([]fusedModule, error) {
	modules := []fusedModule{{path: entry.ModulePath, root: entry.Root}}
	for _, root := range entry.WorkspaceModules {
		data, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("read workspace module %s: %w", root, err)
		}
		path := modfile.ModulePath(data)
		if path == "" {
			return nil, fmt.Errorf("workspace module %s has no module path", root)
		}
		modules = append(modules, fusedModule{path: path, root: root})
	}
	for _, module := range modules {
		if module.path == "" || module.root == "" {
			return nil, fmt.Errorf("module path and root are required")
		}
	}
	slices.SortFunc(modules, func(a, b fusedModule) int {
		return len(b.path) - len(a.path)
	})
	return modules, nil
}

func fusedPackageDir(importPath string, modules []fusedModule) (string, bool) {
	for _, module := range modules {
		relative, ok := strings.CutPrefix(importPath, module.path)
		if !ok || relative != "" && !strings.HasPrefix(relative, "/") {
			continue
		}
		relative = strings.TrimPrefix(relative, "/")
		return filepath.Join(module.root, filepath.FromSlash(relative)), true
	}
	return "", false
}

func inspectFusedPackage(extensionName, dir string) ([]fusedHazard, []string, error) {
	buildContext := build.Default
	pkg, err := buildContext.ImportDir(dir, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect package %s: %w", dir, err)
	}
	files := append(slices.Clone(pkg.GoFiles), pkg.CgoFiles...)
	slices.Sort(files)
	fset := token.NewFileSet()
	var hazards []fusedHazard
	var imports []string
	for _, name := range files {
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
		aliases := make(map[string]string)
		dotImports := make(map[string]bool)
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, nil, fmt.Errorf("parse import in %s: %w", path, err)
			}
			imports = append(imports, importPath)
			alias := filepath.Base(importPath)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			if alias == "." {
				dotImports[importPath] = true
			} else if alias != "_" {
				aliases[alias] = importPath
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			var expression ast.Expr
			switch value := node.(type) {
			case *ast.SelectorExpr:
				expression = value
			case *ast.Ident:
				expression = value
			default:
				return true
			}
			if symbol := fusedHazardSymbol(expression, aliases, dotImports); symbol != "" {
				hazards = append(hazards, fusedHazard{extension: extensionName, position: fset.Position(expression.Pos()), symbol: symbol})
			}
			return true
		})
	}
	return hazards, imports, nil
}

func fusedHazardSymbol(expression ast.Expr, aliases map[string]string, dotImports map[string]bool) string {
	for _, symbol := range []struct {
		path  string
		names []string
	}{
		{path: "os", names: []string{"Exit", "Chdir", "Stdout"}},
		{path: "log", names: []string{"Fatal", "Fatalf", "Fatalln"}},
		{path: "fmt", names: []string{"Print", "Printf", "Println"}},
	} {
		for _, name := range symbol.names {
			selector, ok := expression.(*ast.SelectorExpr)
			if ok && importedSelector(selector, aliases, symbol.path, name) {
				return symbol.path + "." + name
			}
			identifier, ok := expression.(*ast.Ident)
			if ok && identifier.Name == name && identifier.Obj == nil && dotImports[symbol.path] {
				return symbol.path + "." + name
			}
		}
	}
	return ""
}

func importedSelector(selector *ast.SelectorExpr, aliases map[string]string, importPath, name string) bool {
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Obj == nil && aliases[identifier.Name] == importPath && selector.Sel.Name == name
}
