// SPDX-License-Identifier: MIT

package upgrade

import (
	"bytes"
	"go/ast"
	"go/types"
	"strings"
)

// grepReadFileSymbol names the removed member as apidiff reports it.
const grepReadFileSymbol = "GrepOperations.ReadFile"

// planGrepReadFile deletes the ReadFile member an SDK GrepOperations literal still sets. Grep takes its context lines from ripgrep and never read a matched
// file through the member, so deleting it does not change what the tool does. A read of the member (`ops.ReadFile(path)`) has no mechanical form and is left
// to the diagnosis.
func (p *filePlanner) planGrepReadFile() {
	var rejected []types.Error
	for _, typeErr := range p.errors {
		if strings.HasPrefix(typeErr.Msg, "unknown field ReadFile in struct literal") {
			rejected = append(rejected, typeErr)
		}
	}
	if len(rejected) == 0 {
		return
	}
	ast.Inspect(p.file.Syntax, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || !p.isSDKStruct(literal, "GrepOperations") {
			return true
		}
		for i, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok || !p.rejectedAt(rejected, pair.Key.Pos()) {
				continue
			}
			var previous ast.Expr
			if i > 0 {
				previous = literal.Elts[i-1]
			}
			p.sites = append(p.sites, site{
				rewrites: []Rewrite{{Rule: RuleGrepReadFile, Symbol: grepReadFileSymbol, Line: p.line(pair.Pos())}},
				edits:    []edit{p.removeElement(pair, previous)},
			})
		}
		return true
	})
}

// isSDKStruct reports whether literal builds the SDK struct called name.
func (p *filePlanner) isSDKStruct(literal *ast.CompositeLit, name string) bool {
	named, ok := types.Unalias(p.pkg.Info.TypeOf(literal)).(*types.Named)
	return ok && named.Obj().Name() == name && named.Obj().Pkg() != nil && IsSDKPath(named.Obj().Pkg().Path())
}

// removeElement returns the edit that deletes a composite literal's element and the punctuation around it: its comma and, when it fills its lines, those lines.
// The last element of a literal without a trailing comma takes the comma that follows the element before it.
func (p *filePlanner) removeElement(element, previous ast.Expr) edit {
	source := p.file.Source
	start, end := p.offset(element.Pos()), p.offset(element.End())
	skipBlanks := func(at int) int {
		for at < len(source) && (source[at] == ' ' || source[at] == '\t') {
			at++
		}
		return at
	}
	if next := skipBlanks(end); next < len(source) && source[next] == ',' {
		end = next + 1
	} else if previous != nil {
		return edit{start: p.offset(previous.End()), end: end}
	}
	lineStart := start
	for lineStart > 0 && (source[lineStart-1] == ' ' || source[lineStart-1] == '\t') {
		lineStart--
	}
	lineEnd := skipBlanks(end)
	if bytes.HasPrefix(source[lineEnd:], []byte("//")) {
		// A line comment after the element is about it and goes with it.
		if newline := bytes.IndexByte(source[lineEnd:], '\n'); newline >= 0 {
			lineEnd += newline
		} else {
			lineEnd = len(source)
		}
	}
	if (lineStart == 0 || source[lineStart-1] == '\n') && lineEnd < len(source) && source[lineEnd] == '\n' {
		return edit{start: lineStart, end: lineEnd + 1}
	}
	return edit{start: start, end: end}
}
