// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"regexp"
)

const cancelDropSendName = "cancel-drop-send"

// cancelDropSend flags `select { case events <- ev: case <-ctx.Done(): }`
// on an event or listener channel. Once the context is done both cases are
// ready and Go picks one at random, so the terminal message_end, turn_end
// and agent_end of an aborted run are dropped about half the time
// (AGENT-01). Pi awaits every listener, aborted or not.
var cancelDropSend = check{
	Name:    cancelDropSendName,
	Doc:     "event or listener send that races a context-done case and may be dropped",
	Applies: inHot,
	Run:     runCancelDropSend,
}

// eventChanRe matches channel expressions that carry agent, session or
// extension events to their listeners.
var eventChanRe = regexp.MustCompile(`(?i)event|listener|subscri`)

func runCancelDropSend(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		sel, ok := n.(*ast.SelectStmt)
		if !ok {
			return true
		}
		send := selectSend(sel)
		if send == nil || !eventChanRe.MatchString(exprText(fc, send.Chan)) || !selectHasDone(sel) {
			return true
		}
		if h, ok := fc.hit(cancelDropSendName, send.Pos(), append(stack, sel), "send on event channel "+exprText(fc, send.Chan)+" races a Done case, so the event is dropped when the context ends"); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}

// selectSend returns the select's single send case, or nil.
func selectSend(sel *ast.SelectStmt) *ast.SendStmt {
	var send *ast.SendStmt
	for _, s := range sel.Body.List {
		cc, ok := s.(*ast.CommClause)
		if !ok {
			continue
		}
		if s, ok := cc.Comm.(*ast.SendStmt); ok {
			if send != nil {
				return nil
			}
			send = s
		}
	}
	return send
}

// selectHasDone reports a `case <-x.Done():` receive.
func selectHasDone(sel *ast.SelectStmt) bool {
	for _, s := range sel.Body.List {
		cc, ok := s.(*ast.CommClause)
		if !ok || cc.Comm == nil {
			continue
		}
		var recv ast.Expr
		switch c := cc.Comm.(type) {
		case *ast.ExprStmt:
			recv = c.X
		case *ast.AssignStmt:
			if len(c.Rhs) == 1 {
				recv = c.Rhs[0]
			}
		}
		u, ok := recv.(*ast.UnaryExpr)
		if !ok {
			continue
		}
		if call, ok := u.X.(*ast.CallExpr); ok && calleeName(call) == "Done" {
			return true
		}
	}
	return false
}
