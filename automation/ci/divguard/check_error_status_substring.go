// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"regexp"
	"strconv"
)

const errorStatusSubstringName = "error-status-substring"

// errorStatusSubstring flags classifying an error by searching its text for
// an HTTP status code: `strings.Contains(err.Error(), "401")`. Any message
// that merely mentions the digits (a token count, a path, a quoted body)
// is then rewritten or retried as an auth failure (MODES-14, MAIN-09). Pi
// classifies by the provider's status field or its own regexes.
var errorStatusSubstring = check{
	Name:    errorStatusSubstringName,
	Doc:     "error classified by an HTTP status-code substring of its text",
	Applies: inHot,
	Run:     runErrorStatusSubstring,
}

var (
	substringCalls = map[string]bool{"Contains": true, "HasPrefix": true, "HasSuffix": true, "Index": true}
	statusCodeRe   = regexp.MustCompile(`^[1-5][0-9][0-9]$`)
	errTextRe      = regexp.MustCompile(`(?i)err|msg|message`)
)

func runErrorStatusSubstring(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isPkgCall(call, "strings", calleeName(call)) || !substringCalls[calleeName(call)] || len(call.Args) != 2 {
			return true
		}
		code := stringArg(call.Args[1])
		if !statusCodeRe.MatchString(code) || !errTextRe.MatchString(exprText(fc, call.Args[0])) {
			return true
		}
		if h, ok := fc.hit(errorStatusSubstringName, call.Pos(), stack, "classifies an error by the substring "+strconv.Quote(code)+" of its text"); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}
