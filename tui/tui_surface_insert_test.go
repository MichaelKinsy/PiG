package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Container.InsertBefore, which places an extension's custom entry before the
// streaming message (interactive-mode.ts:3834-3840), reports the children it
// kept like every other change of the children. A Remove or Replace of a
// later child in the same frame then splices both changes, and an insert
// alone walks only the inserted child.
func TestTuiSurfaceSplicesAnInsertBefore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(chat *Container, last Component)
	}{
		{name: "alone"},
		{name: "then remove the last child", after: func(chat *Container, last Component) { chat.Remove(last) }},
		{name: "then replace the last child", after: func(chat *Container, last Component) { chat.Replace(last, NewText("replaced")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, c, d := NewText("a"), NewText("b"), NewText("c"), NewText("d")
			chat := NewContainer(a, b, c, d)
			surface, session := startedSurface(t, chat, nil)
			walks := surface.walks
			chat.InsertBefore(c, NewText("inserted"))
			if tc.after != nil {
				tc.after(chat, d)
			}
			surface.Render()
			assertSurfaceIndex(t, surface, 0)
			assertReplayIsFresh(t, surface, session)
			if tc.after == nil && (surface.walks != walks || surface.visited != 1) {
				t.Fatalf("an insert walked the whole document %v and visited %d components, want only the inserted one", surface.walks != walks, surface.visited)
			}
		})
	}
}

// Every Container method that changes the children reports the children it
// kept to the surface (markChildrenLocked). A mutator that only invalidates
// leaves the surface splicing a stale range, so the session's tree diverges
// from the document.
func TestEveryContainerChildMutatorMarksTheKeptChildren(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "tui.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Body == nil {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if ident, ok := star.X.(*ast.Ident); !ok || ident.Name != "Container" {
			continue
		}
		assigns, marks := false, false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if index, ok := lhs.(*ast.IndexExpr); ok {
						lhs = index.X
					}
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "children" {
						assigns = true
					}
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "markChildrenLocked" {
					marks = true
				}
			}
			return true
		})
		if assigns {
			checked++
			if !marks {
				t.Errorf("Container.%s changes the children at %s without markChildrenLocked", fn.Name.Name, fset.Position(fn.Pos()))
			}
		}
	}
	if checked < 6 {
		t.Fatalf("checked %d child mutators, want Add, InsertBefore, Remove, Replace, Clear and SetChildren", checked)
	}
}
