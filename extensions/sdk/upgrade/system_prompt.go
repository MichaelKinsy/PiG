// SPDX-License-Identifier: MIT

package upgrade

import (
	"go/ast"
	"go/constant"
	"go/types"
)

const (
	// systemPromptSince is the SDK release whose before_agent_start data["systemPrompt"] is an *SystemPrompt.
	systemPromptSince = "0.5.0"
	// systemPromptSymbol names the change: the value is not a symbol apidiff can report.
	systemPromptSymbol = `before_agent_start data["systemPrompt"]`
)

// beforeAgentStartData returns the data parameter of each before_agent_start
// handler the package registers with (*Extension).OnEvent, a function literal or
// a function the package declares, when the module requires an SDK before
// 0.5.0. Its data["systemPrompt"] is an *SystemPrompt now: a read as a string
// compiles, yet it panics or, in the ok form, reads an empty prompt.
func (p *filePlanner) beforeAgentStartData() map[*types.Var]bool {
	if p.sdk == "" || !releaseBefore(p.sdkVersion, systemPromptSince) {
		return nil
	}
	data := map[*types.Var]bool{}
	for _, file := range p.pkg.Files {
		ast.Inspect(file.Syntax, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 || !p.isOnEvent(call.Fun) || stringConstant(p.pkg.Info.Types[call.Args[0]]) != "before_agent_start" {
				return true
			}
			var signature *types.Signature
			switch handler := ast.Unparen(call.Args[1]).(type) {
			case *ast.FuncLit:
				signature, _ = p.pkg.Info.TypeOf(handler).(*types.Signature)
			case *ast.Ident:
				if fn, ok := p.pkg.Info.Uses[handler].(*types.Func); ok && fn.Pkg() == p.pkg.Types {
					signature = fn.Signature()
				}
			}
			if signature != nil && signature.Params().Len() == 2 {
				data[signature.Params().At(1)] = true
			}
			return true
		})
	}
	return data
}

// isOnEvent reports whether fun selects the SDK's (*Extension).OnEvent.
func (p *filePlanner) isOnEvent(fun ast.Expr) bool {
	selector, ok := fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OnEvent" {
		return false
	}
	selection := p.pkg.Info.Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal {
		return false
	}
	fn, ok := selection.Obj().(*types.Func)
	return ok && isSDKReceiver(fn.Signature().Recv(), "Extension")
}

// stringConstant returns the value of a string constant, or "" for any other expression.
func stringConstant(tv types.TypeAndValue) string {
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		return ""
	}
	return constant.StringVal(tv.Value)
}

// stringSystemPrompt reports whether assert is data["systemPrompt"].(string)
// for the data parameter of a before_agent_start handler.
func (p *filePlanner) stringSystemPrompt(assert *ast.TypeAssertExpr, data map[*types.Var]bool) bool {
	if len(data) == 0 || assert.Type == nil || !types.Identical(p.pkg.Info.TypeOf(assert.Type), types.Typ[types.String]) {
		return false
	}
	index, ok := ast.Unparen(assert.X).(*ast.IndexExpr)
	if !ok || stringConstant(p.pkg.Info.Types[index.Index]) != "systemPrompt" {
		return false
	}
	ident, ok := ast.Unparen(index.X).(*ast.Ident)
	if !ok {
		return false
	}
	variable, ok := p.pkg.Info.Uses[ident].(*types.Var)
	return ok && data[variable]
}

// systemPromptSite is the read assert, the last node of stack, as a site that
// moves out like a getter: data["systemPrompt"].(*sdk.SystemPrompt).Get()
// returns the prompt and an error. In the ok form, `s, ok := ...` or
// `var s, ok = ...`, the value stands in as `value, true`: before_agent_start's
// data always held the prompt.
func (p *filePlanner) systemPromptSite(assert *ast.TypeAssertExpr, stack []ast.Node) *getterSite {
	commaOK := false
	switch parent := stack[len(stack)-2].(type) {
	case *ast.AssignStmt:
		commaOK = len(parent.Lhs) == 2 && len(parent.Rhs) == 1
	case *ast.ValueSpec:
		commaOK = len(parent.Names) == 2 && len(parent.Values) == 1
	}
	return &getterSite{
		call:    assert,
		getter:  getter{Since: systemPromptSince},
		rule:    RuleSystemPromptGetter,
		symbol:  systemPromptSymbol,
		value:   "systemPrompt",
		text:    p.text(assert.X.Pos(), assert.X.End()) + ".(*" + p.sdk + ".SystemPrompt).Get()",
		commaOK: commaOK,
		stack:   append([]ast.Node(nil), stack...),
		stmtAt:  -1,
	}
}
