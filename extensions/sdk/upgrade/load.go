// SPDX-License-Identifier: MIT

package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// File is one source file of an extension package.
type File struct {
	Path   string
	Source []byte
	Syntax *ast.File
}

// Package is one type-checked package of an extension. Its errors are the
// compiler's own: the package is expected to fail to build against the current
// SDK, and the rewrites read where.
type Package struct {
	ImportPath string
	Fset       *token.FileSet
	Files      []*File
	Types      *types.Package
	Info       *types.Info
	Errors     []types.Error
}

// LoadOptions selects the packages to read.
type LoadOptions struct {
	// Dir is where `go list` runs: the extension's module, or a generated
	// module that imports it. Its go.mod must resolve the current SDK.
	Dir string
	// Roots are the extension's source directories. A package in one is type
	// checked from source and may be rewritten; every other package is read
	// from the compiler's export data.
	Roots []string
	// Flags are go list flags placed before the patterns, such as -modfile.
	Flags []string
	// Patterns are the packages to list. The default is "./...".
	Patterns []string
	// Command is the go command. The default is "go".
	Command string
	// Env is the environment of the go command. The default is the current one.
	Env []string
}

// listedPackage is the part of `go list -json` the loader reads.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	CgoFiles   []string
	Export     string
	ImportMap  map[string]string
	Standard   bool
	Module     *struct {
		Path      string
		GoVersion string
	}
	Error *struct{ Err string }
}

// listPackages runs `go list -e -deps -export`. A package that fails to compile is not an error: the type errors are the product.
func listPackages(ctx context.Context, opts LoadOptions) (listed []listedPackage, roots []string, exports map[string]string, err error) {
	command := opts.Command
	if command == "" {
		command = "go"
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	args := append([]string{"list", "-e", "-deps", "-export", "-json=ImportPath,Dir,GoFiles,CgoFiles,Export,ImportMap,Standard,Module,Error"}, opts.Flags...)
	args = append(args, patterns...)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = opts.Dir
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		// A cancelled listing is cut short: its packages would type check without their dependencies.
		return nil, nil, nil, ctx.Err()
	}
	if runErr != nil && stdout.Len() == 0 {
		return nil, nil, nil, fmt.Errorf("go list: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	decoder := json.NewDecoder(&stdout)
	for {
		var next listedPackage
		if decodeErr := decoder.Decode(&next); errors.Is(decodeErr, io.EOF) {
			break
		} else if decodeErr != nil {
			return nil, nil, nil, fmt.Errorf("read go list output: %w", decodeErr)
		}
		listed = append(listed, next)
	}
	for _, root := range opts.Roots {
		roots = append(roots, canonicalDir(root))
	}
	exports = map[string]string{}
	for _, entry := range listed {
		if entry.Export != "" {
			exports[entry.ImportPath] = entry.Export
		}
	}
	return listed, roots, exports, nil
}

// fromSource reports whether the package is one of the extension's, to be type checked from source.
func (l listedPackage) fromSource(roots []string) bool {
	return !l.Standard && l.Dir != "" && len(l.GoFiles) > 0 && underRoots(canonicalDir(l.Dir), roots)
}

func canonicalDir(dir string) string {
	if physical, err := filepath.EvalSymlinks(dir); err == nil {
		dir = physical
	}
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	return filepath.Clean(dir)
}

func underRoots(dir string, roots []string) bool {
	for _, root := range roots {
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// sourceImporter resolves an import to a package already checked from source,
// else to the compiler's export data.
type sourceImporter struct {
	gc        types.ImporterFrom
	checked   map[string]*types.Package
	importMap map[string]string
}

func (s sourceImporter) Import(path string) (*types.Package, error) { return s.ImportFrom(path, "", 0) }

func (s sourceImporter) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	if mapped, ok := s.importMap[path]; ok {
		path = mapped
	}
	if pkg, ok := s.checked[path]; ok {
		return pkg, nil
	}
	return s.gc.ImportFrom(path, dir, mode)
}

func checkPackage(fset *token.FileSet, importPath string, paths []string, imp types.Importer, goVersion string, overlay map[string][]byte) (*Package, error) {
	pkg := &Package{ImportPath: importPath, Fset: fset}
	syntax := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		data, overlaid := overlay[path]
		if !overlaid {
			var err error
			if data, err = os.ReadFile(path); err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
		}
		parsed, err := parser.ParseFile(fset, path, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		pkg.Files = append(pkg.Files, &File{Path: path, Source: data, Syntax: parsed})
		syntax = append(syntax, parsed)
	}
	pkg.Info = &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes:     map[ast.Node]*types.Scope{},
	}
	config := types.Config{
		Importer:    imp,
		FakeImportC: true,
		GoVersion:   goVersion,
		Error: func(err error) {
			if typeErr, ok := errors.AsType[types.Error](err); ok {
				pkg.Errors = append(pkg.Errors, typeErr)
			}
		},
	}
	// The errors are the product; a package with errors still has the types the rewrites read.
	pkg.Types, _ = config.Check(importPath, fset, syntax, pkg.Info)
	return pkg, nil
}
