// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"path"
	"strings"
)

const agentRunOutsideSessionName = "agent-run-outside-session"

// agentRunOutsideSession flags an agent.Agent Send, SendContent,
// SendMessages or Continue call outside the session layer
// (coding/session*.go). Upstream runs every prompt through AgentSession,
// whose post-run loop drains queued steering and follow-up messages,
// retries, and compacts. Interactive mode driving the agent directly left
// queued input stranded at idle (MODES-01) and forked the retry and
// recovery logic (MODES-08, AGENT-14).
var agentRunOutsideSession = check{
	Name:    agentRunOutsideSessionName,
	Doc:     "agent Send/Continue called outside coding/session*.go",
	Applies: outsideSessionLayer,
	Run:     runAgentRunOutsideSession,
}

var agentRunMethods = map[string]bool{"Send": true, "SendContent": true, "SendMessages": true, "Continue": true}

// outsideSessionLayer reports Go files other than the agent package itself
// and the coding session layer.
func outsideSessionLayer(rel string) bool {
	if strings.HasPrefix(rel, "agent/") && !strings.Contains(strings.TrimPrefix(rel, "agent/"), "/") {
		return false
	}
	return path.Dir(rel) != "coding" || !strings.HasPrefix(path.Base(rel), "session")
}

func runAgentRunOutsideSession(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !agentRunMethods[sel.Sel.Name] || !isAgentReceiver(sel.X) {
			return true
		}
		if h, ok := fc.hit(agentRunOutsideSessionName, call.Pos(), stack, exprText(fc, sel)+" runs the agent outside the session layer, bypassing its post-run queue, retry and compaction loop"); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}

// isAgentReceiver reports a receiver named agent (m.agent, s.Agent, ag).
func isAgentReceiver(e ast.Expr) bool {
	switch name := exprName(e); name {
	case "agent", "Agent", "ag":
		return true
	}
	return false
}
