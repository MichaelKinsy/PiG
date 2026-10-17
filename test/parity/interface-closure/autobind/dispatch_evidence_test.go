package main

import (
	"fmt"
	"go/types"
	"slices"
	"testing"
)

// A test that calls Name() through an interface reaches Widget.Name only when its package can hold a Widget. A test in a package that
// cannot import lib is not evidence for Widget.name, and a dispatching test in an importing package ranks below a test that names
// Widget.Name itself. Red without canHold: the unrelated package's test is evidence for Widget.Name; red without the rank change: the dispatching test ranks first.
func TestDispatchEvidenceNeedsAPackageThatCanHoldTheType(t *testing.T) {
	named := "package %s\n\nimport \"testing\"\n\ntype namer interface{ Name() string }\n\n"
	files := map[string]string{
		// The a* names sort before lib, so a rank tie would put them first.
		"aaunrelated/u.go":      "package aaunrelated\n\ntype Other struct{}\n\nfunc (Other) Name() string { return \"other\" }\n",
		"aaunrelated/u_test.go": fmt.Sprintf(named, "aaunrelated") + "func TestAAOtherName(t *testing.T) {\n\tvar n namer = Other{}\n\tif n.Name() != \"other\" {\n\t\tt.Fatal(\"name\")\n\t}\n}\n",
		"abimporter/i.go":       "package abimporter\n\nimport \"fixture/lib\"\n\nfunc Make() *lib.Widget { return lib.NewWidget(\"w\") }\n",
		"abimporter/i_test.go":  fmt.Sprintf(named, "abimporter") + "func TestABWidgetThroughInterface(t *testing.T) {\n\tvar n namer = Make()\n\tif n.Name() != \"w\" {\n\t\tt.Fatal(\"name\")\n\t}\n}\n",
	}
	fixtureRun(t, files, nil, nil, nil)
	ix := fixtureLast.det.ix
	widget := ix.pkgs["lib"].Types.Scope().Lookup("Widget")
	if widget == nil {
		t.Fatal("no lib.Widget")
	}
	var key string
	if sel := types.NewMethodSet(types.NewPointer(widget.Type())).Lookup(widget.Pkg(), "Name"); sel != nil {
		key = ix.declKey(sel.Obj())
	}
	if key == "" {
		t.Fatal("Widget.Name not found")
	}
	var refs []string
	for _, ev := range ix.testsFor([]string{key}) {
		refs = append(refs, ev.Ref)
	}
	want := []string{"test:lib/lib_test.go#TestWidget", "test:abimporter/i_test.go#TestABWidgetThroughInterface"}
	if !slices.Equal(refs, want) {
		t.Fatalf("Widget.Name evidence = %v, want %v", refs, want)
	}
}
