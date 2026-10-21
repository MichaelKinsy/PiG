// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"
)

// rpcWriterScope reports whether a production file writes RPC stdout or an extension socket: coding/cli/rpc*.go and coding/extension/host/subprocess. A package outside the module (analysistest fixtures) is in scope.
func rpcWriterScope(pkgPath, file string) bool {
	if strings.HasSuffix(file, "_test.go") {
		return false
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(pkgPath, modulePath), "_test")
	switch rel {
	case pkgPath, "/coding/extension/host/subprocess":
		return true
	case "/coding/cli":
		return strings.HasPrefix(filepath.Base(file), "rpc")
	}
	return false
}

// outsideSinkReason says which writer call reaches RPC stdout or an extension socket, or "".
func (p *Pass) outsideSinkReason(call *ast.CallExpr) string {
	fn := p.Callee(call)
	if fn == nil || fn.Pkg() == nil {
		return ""
	}
	sig, _ := fn.Type().(*types.Signature)
	switch name := fn.Name(); {
	case name == "RawStdoutWriter" && fn.Pkg().Path() == modulePath+"/internal/codingagent":
		return "codingagent.RawStdoutWriter hands out the raw stdout writer"
	case name == "writeJSONLine" && fn.Pkg() == p.Pkg:
		return "writeJSONLine writes a record to its writer, in no order but its caller's"
	case name == "writeInline" && sig != nil && sig.Recv() != nil && namedTypeName(sig.Recv().Type()) == "Conn":
		return "(*Conn).writeInline writes a frame on the caller's goroutine"
	case name == "Write" && sig != nil && sig.Recv() != nil && (isNetConn(sig.Recv().Type()) || p.staticRecvIsNetConn(call)):
		return "a net.Conn write puts a frame on the socket from the caller's goroutine"
	}
	return ""
}

// staticRecvIsNetConn reports whether call's receiver expression has a net connection type: a net.UnixConn inherits Write from an unexported type of package net.
func (p *Pass) staticRecvIsNetConn(call *ast.CallExpr) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	s := p.TypesInfo.Selections[sel]
	return s != nil && isNetConn(s.Recv())
}

// namedTypeName is the name of t after pointer indirection, or "".
func namedTypeName(t types.Type) string {
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if n, ok := types.Unalias(t).(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

// isNetConn reports whether t is net.Conn or one of the net package's concrete connections.
func isNetConn(t types.Type) bool {
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		t = ptr.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "net" && strings.HasSuffix(n.Obj().Name(), "Conn")
}

// RPCWriterOutsideSink flags a second writer of RPC stdout or of an extension socket.
var RPCWriterOutsideSink = newOrderingCheck("rpcwriteroutsidesink",
	"in coding/cli/rpc*.go and coding/extension/host/subprocess, a reference to os.Stdout or a call to codingagent.RawStdoutWriter, writeJSONLine, a net.Conn Write or (*Conn).writeInline: one writer owns each stream, so a record written anywhere else can overtake what the owner has queued; the owner carries a marker with an executor: class",
	High, "B4: extension output reached the raw stdout writer while a command's response still waited behind the microtask queue",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.SelectorExpr)(nil), (*ast.CallExpr)(nil)}, func(n ast.Node) {
			if !rpcWriterScope(p.Pkg.Path(), p.Fset.Position(n.Pos()).Filename) {
				return
			}
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if v, ok := p.TypesInfo.Uses[n.Sel].(*types.Var); ok && v.Pkg() != nil && v.Pkg().Path() == "os" && v.Name() == "Stdout" {
					p.Reportf(n.Pos(), "os.Stdout is written outside the RPC writer; a record written here can overtake the queued one")
				}
			case *ast.CallExpr:
				if reason := p.outsideSinkReason(n); reason != "" {
					p.Reportf(n.Pos(), "%s; only the stream's owner writes it", reason)
				}
			}
		})
	})
