package wiring

import (
	"go/ast"
	"go/types"
	"reflect"
	"testing"
)

// registerDeclarations are the register-frame declaration lists whose every field must carry a value some host code
// reads. A field no host code reads is a dead wire field: every SDK sends it and it changes nothing (wiring-checks F15).
var registerDeclarations = []string{"CommandDecl", "HandlerDecl"}

// TestRegisterDeclarationFieldsAreRead reads the host from the code that decodes the register frame: each field of a
// command or handler declaration must be selected by non-test host code outside the declaration. The Go host decodes the
// frame strictly, so a field the host never reads is a field a sender must still emit to be accepted.
func TestRegisterDeclarationFieldsAreRead(t *testing.T) {
	host := goPkg(t, "coding/extension/host/subprocess")
	read := map[*types.Var]string{}
	host.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return
		}
		if s := host.TypesInfo.Selections[sel]; s != nil && s.Kind() == types.FieldVal {
			if field, ok := s.Obj().(*types.Var); ok {
				read[field.Origin()] = host.position(sel.Pos())
			}
		}
	})
	l := newLedger(t, "register-declaration-field-read", nil)
	for _, name := range registerDeclarations {
		st := host.named(t, name).Underlying().(*types.Struct)
		for i := range st.NumFields() {
			field := st.Field(i)
			if !field.Exported() || reflect.StructTag(st.Tag(i)).Get("json") == "-" {
				continue
			}
			if read[field] == "" {
				l.add(name+"."+field.Name(), name+"."+field.Name()+" is decoded from the register frame and read by no host code")
			}
		}
	}
	l.finish()
}
