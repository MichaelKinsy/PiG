package rules

import (
	"encoding/json"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"
)

// ASTTags is the TagResolver of rule U6. It reads the discriminator value of a Go struct from, in order:
//
//	a method named after the discriminator (Type for "type") that has no parameter and returns one string constant;
//	else the one string constant returned by the parameterless methods named <Kind><Discriminator> (AnswerType, EventType, or the
//	sealing method storageWriteType for "type"), when they agree; or
//	the string constants of MarshalJSON, when the body names the discriminator key (as a constant, a JSON object constant or a
//	`json:"key"` struct tag) and holds exactly one other string constant, which is the value.
//
// A value that the code computes, or a MarshalJSON that holds several candidates, is not readable and the resolver reports ok=false.
type ASTTags struct {
	files []*ast.File
	info  *types.Info
}

// NewASTTags returns a resolver over the files of one type-checked package; info needs Types and Uses.
func NewASTTags(files []*ast.File, info *types.Info) *ASTTags {
	return &ASTTags{files: files, info: info}
}

// Tag implements TagResolver.
func (a *ASTTags) Tag(impl *types.Named, discriminator string) (value, evidence string, ok bool) {
	suffixed := map[string]string{} // value -> method, for methods named <Kind><Discriminator> (AnswerType, EventType)
	for _, f := range a.files {
		for _, decl := range f.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil || !a.receiverIs(fn.Recv.List[0].Type, impl) || fn.Type.Params.NumFields() != 0 {
				continue
			}
			name, key := ouFoldName(fn.Name.Name), ouFoldName(discriminator)
			if suffix := strings.HasSuffix(name, key); name != key && !suffix {
				continue
			}
			if v, found := a.returnedConst(fn); found {
				if name == key {
					return v, "method " + fn.Name.Name, true
				}
				suffixed[v] = fn.Name.Name
			}
		}
	}
	if len(suffixed) == 1 {
		for v, name := range suffixed {
			return v, "method " + name, true
		}
	}
	for _, f := range a.files {
		for _, decl := range f.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil || fn.Name.Name != "MarshalJSON" || !a.receiverIs(fn.Recv.List[0].Type, impl) {
				continue
			}
			if v, found := a.marshalValue(fn, discriminator); found {
				return v, "MarshalJSON", true
			}
		}
	}
	return "", "", false
}

func (a *ASTTags) receiverIs(expr ast.Expr, impl *types.Named) bool {
	for {
		switch x := expr.(type) {
		case *ast.StarExpr:
			expr = x.X
			continue
		case *ast.ParenExpr:
			expr = x.X
			continue
		case *ast.Ident:
			if obj := a.info.Uses[x]; obj != nil {
				return obj == impl.Obj()
			}
			return x.Name == impl.Obj().Name()
		}
		return false
	}
}

func (a *ASTTags) constString(e ast.Expr) (string, bool) {
	if tv, ok := a.info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value), true
	}
	return "", false
}

func (a *ASTTags) returnedConst(fn *ast.FuncDecl) (string, bool) {
	if len(fn.Body.List) != 1 {
		return "", false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", false
	}
	return a.constString(ret.Results[0])
}

func (a *ASTTags) marshalValue(fn *ast.FuncDecl, key string) (string, bool) {
	consts := map[string]bool{}
	keyNamed := false
	note := func(s string) {
		var obj map[string]any
		if json.Unmarshal([]byte(s), &obj) == nil {
			if v, ok := obj[key].(string); ok {
				keyNamed = true
				consts[v] = true
			}
			return
		}
		if s == key {
			keyNamed = true
			return
		}
		consts[s] = true
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && (strings.Contains(lit.Value, `json:"`+key+`"`) || strings.Contains(lit.Value, `json:"`+key+`,`)) {
			keyNamed = true
			return true
		}
		if e, ok := n.(ast.Expr); ok {
			if s, ok := a.constString(e); ok {
				note(s)
			}
		}
		return true
	})
	if !keyNamed || len(consts) != 1 {
		return "", false
	}
	for s := range consts {
		return s, true
	}
	return "", false
}
