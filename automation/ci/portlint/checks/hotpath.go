// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/types"
	"strings"
)

// hotpathDirective marks a function that runs once per event, frame, token or turn. The passes below flag the allocations a hot path must not make.
const hotpathDirective = "//pig:hotpath"

// hotpathFunc reports whether the function declaration carries the directive in its doc comment.
func hotpathFunc(fn *ast.FuncDecl) bool {
	if fn.Doc == nil || fn.Body == nil {
		return false
	}
	for _, comment := range fn.Doc.List {
		if rest, ok := strings.CutPrefix(comment.Text, hotpathDirective); ok && (rest == "" || rest[0] == ' ') {
			return true
		}
	}
	return false
}

// eachHotpathNode calls visit for every node inside a //pig:hotpath function, closures included: a closure built on the hot path runs on it.
func (p *Pass) eachHotpathNode(visit func(ast.Node)) {
	p.Inspect.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		fn := n.(*ast.FuncDecl)
		if p.IsTest(fn.Pos()) || !hotpathFunc(fn) {
			return
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n != nil {
				visit(n)
			}
			return true
		})
	})
}

// anyMap reports whether t is a map from string to the empty interface.
func anyMap(t types.Type) bool {
	m, ok := t.Underlying().(*types.Map)
	if !ok {
		return false
	}
	key, ok := m.Key().Underlying().(*types.Basic)
	if !ok || key.Kind() != types.String {
		return false
	}
	elem, ok := m.Elem().Underlying().(*types.Interface)
	return ok && elem.Empty()
}

// HotpathJSON flags reflection JSON and map[string]any on a function marked //pig:hotpath (pattern P3: typed encoding).
var HotpathJSON = newCheck("hotpathjson",
	"reflection JSON (Marshal, Unmarshal, NewEncoder, NewDecoder) or a map[string]any built on a //pig:hotpath function: every event, frame or turn pays the reflection walk and the map; append the bytes with a typed encoder and decode into a struct",
	Med, "complexity-audit hotpath-sweep P3: rpcAgentEvent, makeEventHandler and the extension frame encoders marshal through reflection per event",
	func(p *Pass) {
		p.eachHotpathNode(func(n ast.Node) {
			switch n := n.(type) {
			case *ast.CallExpr:
				if p.callsCodec(n, jsonEncoders) || p.callsCodec(n, jsonDecoders) {
					fn := p.Callee(n)
					p.Reportf(n.Pos(), "%s.%s on a hot path walks the value by reflection on every call", fn.Pkg().Name(), fn.Name())
					return
				}
				if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok && id.Name == "make" && len(n.Args) > 0 {
					if tv, ok := p.TypesInfo.Types[n.Args[0]]; ok && tv.IsType() && anyMap(tv.Type) {
						p.Reportf(n.Pos(), "make(map[string]any) on a hot path allocates a map per call")
					}
				}
			case *ast.CompositeLit:
				if tv, ok := p.TypesInfo.Types[n]; ok && anyMap(tv.Type) {
					p.Reportf(n.Pos(), "a map[string]any literal on a hot path allocates a map per call")
				}
			}
		})
	})

// copiers are the standard helpers that copy a slice or map into new storage.
var copiers = map[string]map[string]bool{
	"slices": {"Clone": true},
	"maps":   {"Clone": true},
	"bytes":  {"Clone": true},
}

// formatters build a string by reflection over their operands.
var formatters = map[string]map[string]bool{
	"fmt": {"Sprintf": true, "Sprint": true, "Sprintln": true},
}

// timers allocate a timer and a channel per call.
var timers = map[string]map[string]bool{
	"time": {"After": true, "NewTimer": true, "Tick": true},
}

// HotpathAlloc flags per-call allocation a //pig:hotpath function can share or pool (pattern P5: reuse).
var HotpathAlloc = newCheck("hotpathalloc",
	"formatting (fmt.Sprint*), a slice or map copy (slices.Clone, maps.Clone, bytes.Clone, append([]T(nil), ...)) or a timer (time.After, time.NewTimer) on a //pig:hotpath function: every event, frame or turn allocates what could be built once, shared by reference or pooled",
	Low, "complexity-audit hotpath-sweep P5: three clones of every published message per event, a timer per wait",
	func(p *Pass) {
		p.eachHotpathNode(func(n ast.Node) {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return
			}
			switch {
			case p.callsCodec(call, formatters):
				p.Reportf(call.Pos(), "fmt.%s on a hot path formats by reflection and allocates the result on every call", p.Callee(call).Name())
			case p.callsCodec(call, copiers):
				fn := p.Callee(call)
				p.Reportf(call.Pos(), "%s.%s on a hot path copies into new storage on every call", fn.Pkg().Name(), fn.Name())
			case p.callsCodec(call, timers):
				p.Reportf(call.Pos(), "time.%s on a hot path allocates a timer and a channel on every call; re-arm one timer", p.Callee(call).Name())
			case copiesByAppend(call):
				p.Reportf(call.Pos(), "append([]T(nil), x...) on a hot path copies x into new storage on every call")
			}
		})
	})

// copiesByAppend matches append([]T(nil), x...): a slice copy spelled as an append to a nil slice.
func copiesByAppend(call *ast.CallExpr) bool {
	id, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok || id.Name != "append" || len(call.Args) != 2 || call.Ellipsis == 0 {
		return false
	}
	conv, ok := ast.Unparen(call.Args[0]).(*ast.CallExpr)
	if !ok || len(conv.Args) != 1 {
		return false
	}
	nilArg, ok := ast.Unparen(conv.Args[0]).(*ast.Ident)
	_, isSlice := ast.Unparen(conv.Fun).(*ast.ArrayType)
	return ok && nilArg.Name == "nil" && isSlice
}
