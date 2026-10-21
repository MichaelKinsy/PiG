// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/types"
	"strings"
)

// fanInNamePrefixes start the name of a function that sends a value to someone: calling one for each entry of a map delivers the entries in the map's random order.
var fanInNamePrefixes = []string{"emit", "publish", "notify", "deliver", "dispatch", "send", "reply", "write", "fire"}

// callName is the name of the function or method call invokes, or "".
func (p *Pass) callName(call *ast.CallExpr) string {
	if fn := p.Callee(call); fn != nil {
		return fn.Name()
	}
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// fanInEffect says what body does for one map entry that others can see, or "": it sends on a channel, invokes a callback (callbackReason) or calls a function named like a send. A nested function literal is not part of the iteration.
func (p *Pass) fanInEffect(body *ast.BlockStmt) string {
	reason := ""
	ast.Inspect(body, func(n ast.Node) bool {
		if reason != "" {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.SendStmt:
			reason = "sends on a channel"
		case *ast.CallExpr:
			if reason = p.callbackReason(n, body.Pos(), body.End()); reason != "" {
				return false
			}
			name := strings.ToLower(p.callName(n))
			for _, prefix := range fanInNamePrefixes {
				if strings.HasPrefix(name, prefix) {
					reason = "calls " + p.callName(n)
					break
				}
			}
		}
		return reason == ""
	})
	return reason
}

// UnorderedFanIn flags a map range that feeds output at the extension boundary.
var UnorderedFanIn = newOrderingCheck("unorderedfanin",
	"range over a map in an extension-boundary package whose body sends on a channel, invokes a callback or an extension interface method, or calls a function named emit*, publish*, notify*, deliver*, dispatch*, send*, reply*, write* or fire*: Go randomizes the order while Pi's Map and objects keep insertion order; range an ordered slice, or slices.Sorted(maps.Keys(m)) where Pi sorts. The select half (two receive cases on data channels) is not checked",
	High, "ui_bridge re-applied a status map to a new UI context in Go map order (a candidate: Pi's Map keeps insertion order)",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.RangeStmt)(nil)}, func(n ast.Node) {
			r := n.(*ast.RangeStmt)
			if !p.inOrderingScope(r.Pos()) {
				return
			}
			t := p.TypesInfo.TypeOf(r.X)
			if t == nil {
				return
			}
			if _, ok := types.Unalias(t).Underlying().(*types.Map); !ok {
				return
			}
			if reason := p.fanInEffect(r.Body); reason != "" {
				p.Reportf(r.Pos(), "range over a map %s: entries arrive in random order, where Pi keeps insertion order", reason)
			}
		})
	})
