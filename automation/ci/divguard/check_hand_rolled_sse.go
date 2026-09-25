// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"strconv"
)

const handRolledSSEName = "hand-rolled-sse"

// handRolledSSE flags Server-Sent Events field parsing outside one shared
// decoder: a strings or bytes prefix test, trim or cut on "data:" or
// "event:". Each provider's own line loop accepted only "data: " with a
// space and never joined multi-line data, so a gateway writing `data:{...}`
// produced empty turns (AI-07). Pi's decoders follow the SSE spec. The
// shared decoder lives in ai/sse.go, the one file allowed to parse fields.
var handRolledSSE = check{
	Name:    handRolledSSEName,
	Doc:     "SSE data:/event: field parsing outside the shared decoder (ai/sse.go)",
	Applies: func(rel string) bool { return inHot(rel) && rel != "ai/sse.go" },
	Run:     runHandRolledSSE,
}

var (
	prefixCalls = map[string]bool{"HasPrefix": true, "TrimPrefix": true, "CutPrefix": true}
	sseFields   = map[string]bool{"data:": true, "data: ": true, "event:": true, "event: ": true}
	// lineRe keeps the check on stream lines and off `data:` URLs.
	lineRe = regexp.MustCompile(`(?i)line`)
)

func runHandRolledSSE(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !prefixCalls[calleeName(call)] || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || (pkg.Name != "strings" && pkg.Name != "bytes") {
			return true
		}
		field := stringArg(call.Args[1])
		if !sseFields[field] || !lineRe.MatchString(exprText(fc, call.Args[0])) {
			return true
		}
		if h, ok := fc.hit(handRolledSSEName, call.Pos(), stack, "parses the SSE "+strconv.Quote(field)+" field by hand; use the shared decoder in "+path.Join("ai", "sse.go")); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}

// stringArg is the value of a string literal or []byte("...") conversion.
func stringArg(e ast.Expr) string {
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
		e = call.Args[0]
	}
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return v
}
