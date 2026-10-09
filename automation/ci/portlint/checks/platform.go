// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// fsFuncs are the os functions whose first argument is a path the call creates, writes or removes.
var fsFuncs = map[string]bool{"MkdirAll": true, "Mkdir": true, "WriteFile": true, "Create": true, "RemoveAll": true, "Remove": true, "MkdirTemp": true, "CreateTemp": true, "Chdir": true, "Rename": true, "OpenFile": true, "Symlink": true}

// HardTmp flags a literal /tmp path handed to a filesystem call.
var HardTmp = newToolingCheck("hardtmp",
	"literal /tmp or /var/tmp path passed to an os filesystem call: use t.TempDir, os.TempDir or the scoped test temporary directory",
	Low, "Windows and scoped-TMPDIR tests",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			call := n.(*ast.CallExpr)
			fn := p.Callee(call)
			if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "os" || !fsFuncs[fn.Name()] || len(call.Args) == 0 {
				return
			}
			for _, arg := range call.Args {
				s, ok := StringLit(arg)
				if !ok {
					continue
				}
				for _, root := range []string{"/tmp", "/var/tmp"} {
					if s == root || strings.HasPrefix(s, root+"/") {
						p.Reportf(arg.Pos(), "hard-coded %s path in os.%s", root, fn.Name())
					}
				}
			}
		})
	})

// importsOf lists the import paths of the file containing pos.
func (p *Pass) importsOf(pos token.Pos) map[string]bool {
	out := map[string]bool{}
	if f := p.fileOf(pos); f != nil {
		for _, im := range f.Imports {
			s, _ := strconv.Unquote(im.Path.Value)
			out[s] = true
		}
	}
	return out
}

var pathFuncs = map[string]bool{"Join": true, "Dir": true, "Base": true, "Clean": true, "Ext": true, "Split": true, "IsAbs": true}

// PathSeparators flags the slash-only path package and \n-only line splitting in code that handles files.
var PathSeparators = newCheck("pathseparators",
	"path.Join and friends, or strings.Split on \"\\n\", in a file that reads or writes files: use filepath, and split on \\r?\\n like Pi",
	Low, "Windows paths and CRLF files",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			call := n.(*ast.CallExpr)
			if p.IsTest(call.Pos()) {
				return
			}
			imps := p.importsOf(call.Pos())
			if !imps["os"] || imps["net/url"] || imps["net/http"] {
				return
			}
			if fn := p.Callee(call); fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "path" && pathFuncs[fn.Name()] {
				p.Reportf(call.Pos(), "path.%s on a file path splits on / only; use filepath", fn.Name())
			}
			if (p.IsFunc(call, "strings", "Split") || p.IsFunc(call, "strings", "SplitSeq")) && len(call.Args) == 2 {
				if sep, ok := StringLit(call.Args[1]); ok && sep == "\n" {
					p.Reportf(call.Pos(), "splitting on \\n leaves a trailing \\r on CRLF input; Pi splits on /\\r?\\n/")
				}
			}
		})
	})

// windowsBuilds reports whether the file is compiled for Windows according to its name and build constraint.
func (p *Pass) windowsBuilds(pos token.Pos) bool {
	name := p.Fset.Position(pos).Filename
	base := strings.TrimSuffix(strings.TrimSuffix(name[strings.LastIndex(name, "/")+1:], ".go"), "_test")
	for _, os := range []string{"_linux", "_darwin", "_unix", "_freebsd", "_posix"} {
		if strings.HasSuffix(base, os) {
			return false
		}
	}
	if f := p.fileOf(pos); f != nil {
		for _, cg := range f.Comments {
			if cg.Pos() > f.Package {
				break
			}
			for _, c := range cg.List {
				if expr, ok := strings.CutPrefix(c.Text, "//go:build "); ok {
					if strings.Contains(expr, "!windows") || (!strings.Contains(expr, "windows") && !strings.Contains(expr, "!")) {
						return false
					}
				}
			}
		}
	}
	return true
}

// RawSymlink flags os.Symlink in a test that builds for Windows, where the call needs a privilege.
var RawSymlink = newToolingCheck("rawsymlink",
	"os.Symlink in a test that builds for Windows: use testenv.Symlink or testenv.RequireDirectoryLink",
	Low, "Windows-built tests without the symlink privilege (folds the testenv symlink rule)",
	func(p *Pass) {
		if strings.HasSuffix(p.Pkg.Path(), "/internal/testenv") {
			return
		}
		p.Inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			call := n.(*ast.CallExpr)
			if p.IsTest(call.Pos()) && p.IsFunc(call, "os", "Symlink") && p.windowsBuilds(call.Pos()) {
				p.Reportf(call.Pos(), "os.Symlink in a Windows-built test; use testenv.Symlink")
			}
		})
	})

// UnixSocketPath flags a unix socket under a long temporary directory path.
var UnixSocketPath = newToolingCheck("unixsocketpath",
	"unix socket address built under t.TempDir or os.TempDir: sun_path holds 104 bytes on macOS and 108 on Linux; use testenv.ShortTempDir",
	Med, "unix socket paths over 104 bytes",
	func(p *Pass) {
		p.Funcs(func(_ string, _ *ast.FuncType, body *ast.BlockStmt) {
			var socks []*ast.CallExpr
			longDir := false
			shortDir := false
			ast.Inspect(body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.FuncLit); ok && lit.Body != body {
					return false
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if fn := p.Callee(call); fn != nil {
					switch fn.Name() {
					case "TempDir":
						longDir = true
					case "ShortTempDir":
						shortDir = true
					}
				}
				if (p.IsFunc(call, "net", "Listen") || p.IsFunc(call, "net", "Dial") || p.IsFunc(call, "net", "DialTimeout")) && len(call.Args) > 0 {
					if network, ok := StringLit(call.Args[0]); ok && strings.HasPrefix(network, "unix") {
						socks = append(socks, call)
					}
				}
				return true
			})
			if longDir && !shortDir {
				for _, s := range socks {
					p.Reportf(s.Pos(), "unix socket in a function that builds paths under TempDir; the path can exceed the 104-byte limit")
				}
			}
		})
	})
