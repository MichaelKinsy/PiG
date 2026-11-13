// SPDX-License-Identifier: MIT

package upgrade

import (
	"go/ast"
	"go/types"
	"strings"

	impl "github.com/MichaelKinsy/PiG/extensions/sdk/upgrade/internal/upgrade"
)

// planSetModelNames rewrites ctx.SetModel(name) to ctx.SetModelNamed(name) where name is a string. SDK 0.8.0's SetModel takes the model object, as
// Pi's pi.setModel(model) does, and SetModelNamed keeps the `provider/id` form with its results. A rejected argument that SetModelNamed would not
// take either (an any, a defined string type, bytes) is listed: whether it holds a name or a model, and how to convert it, is the author's to say. A call that passes a map compiles and is not touched.
func (p *filePlanner) planSetModelNames() {
	var rejected []types.Error
	for _, typeErr := range p.errors {
		if strings.HasPrefix(typeErr.Msg, "cannot use ") && strings.Contains(typeErr.Msg, " as map[string]any value in argument to ") {
			rejected = append(rejected, typeErr)
		}
	}
	if len(rejected) == 0 {
		return
	}
	ast.Inspect(p.file.Syntax, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || !p.rejectedAt(rejected, call.Args[0].Pos()) {
			return true
		}
		selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SetModel" || !p.isContextMethod(selector) {
			return true
		}
		if argument := p.pkg.Info.TypeOf(call.Args[0]); argument == nil || !types.AssignableTo(argument, types.Typ[types.String]) {
			p.skip(RuleSetModelObject, impl.SetModelSymbol, call.Args[0].Pos(), "the argument is not a string: pass SetModel a model object, or pass the `provider/id` name to SetModelNamed as a string")
			return true
		}
		p.sites = append(p.sites, site{
			rewrites: []Rewrite{{Rule: RuleSetModelObject, Symbol: impl.SetModelSymbol, Line: p.line(call.Args[0].Pos())}},
			edits:    []edit{{start: p.offset(selector.Sel.Pos()), end: p.offset(selector.Sel.End()), text: "SetModelNamed"}},
		})
		return true
	})
}
