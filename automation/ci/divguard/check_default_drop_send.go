// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"slices"
)

const defaultDropSendName = "default-drop-send"

// defaultDropSend flags a non-blocking send, `select { case ch <- v:
// default: }`. When the channel is full the value is dropped silently: UI
// tasks (TUI-03), extension messages (MODES-09) and outbound frames
// (EXT-17). Pi has no bounded queue that drops work. Three idioms that lose
// nothing are exempt: a coalescing wake-up (`ch <- struct{}{}`), a provably
// one-shot send on a capacity-1 result channel (see isOneShot), and a
// latest-value slot drained by the statement just before the send.
var defaultDropSend = check{
	Name:    defaultDropSendName,
	Doc:     "non-blocking send that silently drops its value when the channel is full",
	Applies: inHot,
	Run:     runDefaultDropSend,
}

func runDefaultDropSend(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		sel, ok := n.(*ast.SelectStmt)
		if !ok {
			return true
		}
		send := selectSend(sel)
		if send == nil || !selectHasDefault(sel) || isWakeToken(send.Value) || isOneShot(stack, send.Chan) || drainedBefore(fc, stack, sel, send.Chan) {
			return true
		}
		if h, ok := fc.hit(defaultDropSendName, send.Pos(), append(stack, sel), "non-blocking send on "+exprText(fc, send.Chan)+" drops the value when the channel is full"); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}

func selectHasDefault(sel *ast.SelectStmt) bool {
	for _, s := range sel.Body.List {
		if cc, ok := s.(*ast.CommClause); ok && cc.Comm == nil {
			return true
		}
	}
	return false
}

// isOneShot reports a send that can fire at most once per channel: the
// channel is a local bound exactly once in the enclosing function, to
// `make(chan T, 1)`, it is the channel's only send, and the send sits in that
// same function body (not in a closure that may run many times) with no loop
// between the binding and the send. Anything weaker, such as a capacity-1
// queue fed from a loop or multiple send sites, a callback, or a name rebound
// or shadowed elsewhere in the function, needs an explicit allow marker.
func isOneShot(stack []ast.Node, ch ast.Expr) bool {
	id, ok := ch.(*ast.Ident)
	if !ok {
		return false
	}
	fnAt := -1
	for i, n := range stack {
		if _, ok := n.(*ast.FuncDecl); ok {
			fnAt = i
		}
	}
	if fnAt < 0 {
		return false
	}
	fn := stack[fnAt].(*ast.FuncDecl)
	binding, count := bindings(fn, id.Name)
	if count != 1 || binding == nil {
		return false
	}
	call, ok := binding.rhs.(*ast.CallExpr)
	if !ok || !isMakeChanCap(call, "1") || !hasSingleSafeSend(fn, id.Name, binding.pos) {
		return false
	}
	for _, n := range stack[fnAt+1:] {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ForStmt, *ast.RangeStmt:
			if binding.pos < n.Pos() || binding.pos >= n.End() {
				return false
			}
		}
	}
	return true
}

// hasSingleSafeSend reports whether name has exactly one send and never
// escapes through another expression. Receives and returning the channel are
// safe: neither can prefill it before the candidate send. An alias, call
// argument, stored channel, or second blocking or non-blocking send makes the
// proof fail closed.
func hasSingleSafeSend(fn *ast.FuncDecl, name string, binding token.Pos) bool {
	sends := 0
	safe := true
	inspect(fn.Body, func(n ast.Node, stack []ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name || id.Pos() == binding {
			return true
		}
		if len(stack) == 0 {
			safe = false
			return true
		}
		parent := stack[len(stack)-1]
		switch p := parent.(type) {
		case *ast.SendStmt:
			if p.Chan == id {
				sends++
			} else {
				safe = false
			}
		case *ast.UnaryExpr:
			if p.Op != token.ARROW || p.X != id {
				safe = false
			}
		case *ast.RangeStmt:
			if p.X != id {
				safe = false
			}
		case *ast.ReturnStmt:
			insideClosure := slices.ContainsFunc(stack, func(n ast.Node) bool {
				_, ok := n.(*ast.FuncLit)
				return ok
			})
			if insideClosure || !slices.Contains(p.Results, ast.Expr(id)) {
				safe = false
			}
		default:
			safe = false
		}
		return true
	})
	return safe && sends == 1
}

type bindingSite struct {
	pos token.Pos
	rhs ast.Expr
}

// bindings counts every place fn binds or assigns name (parameters,
// results, receivers, var specs, assignments, range variables) and returns
// the site when there is exactly one.
func bindings(fn *ast.FuncDecl, name string) (*bindingSite, int) {
	var site *bindingSite
	count := 0
	add := func(id ast.Expr, rhs ast.Expr) {
		if i, ok := id.(*ast.Ident); ok && i.Name == name {
			count++
			site = &bindingSite{pos: i.Pos(), rhs: rhs}
		}
	}
	for _, fl := range []*ast.FieldList{fn.Recv, fn.Type.Params, fn.Type.Results} {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				add(n, nil)
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				var rhs ast.Expr
				if len(n.Lhs) == len(n.Rhs) {
					rhs = n.Rhs[i]
				}
				add(lhs, rhs)
			}
		case *ast.ValueSpec:
			for i, nm := range n.Names {
				var rhs ast.Expr
				if i < len(n.Values) {
					rhs = n.Values[i]
				}
				add(nm, rhs)
			}
		case *ast.RangeStmt:
			add(n.Key, nil)
			add(n.Value, nil)
		case *ast.FuncLit:
			for _, f := range n.Type.Params.List {
				for _, nm := range f.Names {
					add(nm, nil)
				}
			}
		}
		return true
	})
	if count != 1 {
		return nil, count
	}
	return site, count
}

func isMakeChanCap(call *ast.CallExpr, capacity string) bool {
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != "make" || len(call.Args) != 2 {
		return false
	}
	if _, ok := call.Args[0].(*ast.ChanType); !ok {
		return false
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	return ok && lit.Value == capacity
}

// drainedBefore reports that the statement before sel in its block is a
// non-blocking receive from the same channel, which makes the send replace
// the channel's latest value.
func drainedBefore(fc *fileCtx, stack []ast.Node, sel *ast.SelectStmt, ch ast.Expr) bool {
	if len(stack) == 0 {
		return false
	}
	block, ok := stack[len(stack)-1].(*ast.BlockStmt)
	if !ok {
		return false
	}
	for i, st := range block.List {
		if st != sel || i == 0 {
			continue
		}
		prev, ok := block.List[i-1].(*ast.SelectStmt)
		if !ok || !selectHasDefault(prev) {
			return false
		}
		for _, c := range prev.Body.List {
			cc, ok := c.(*ast.CommClause)
			if !ok || cc.Comm == nil {
				continue
			}
			if es, ok := cc.Comm.(*ast.ExprStmt); ok {
				if u, ok := es.X.(*ast.UnaryExpr); ok && exprText(fc, u.X) == exprText(fc, ch) {
					return true
				}
			}
		}
	}
	return false
}

// isWakeToken reports `struct{}{}`, the value of a coalescing wake-up send.
func isWakeToken(e ast.Expr) bool {
	lit, ok := e.(*ast.CompositeLit)
	if !ok || len(lit.Elts) != 0 {
		return false
	}
	st, ok := lit.Type.(*ast.StructType)
	return ok && (st.Fields == nil || len(st.Fields.List) == 0)
}
