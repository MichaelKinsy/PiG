package runtimecell

import "testing"

// A //go:embed directive inside a parenthesized var block is indented by gofmt, and its arguments may be quoted. go/build reads every go:embed line comment token (go/build/read.go:readGoInfo), so the files those directives select belong to the module's source set.
func TestHashGoModuleTracksIndentedAndQuotedEmbedDirectives(t *testing.T) {
	root := "package m\n\nimport \"embed\"\n\nvar (\n" +
		"\t//go:embed tab.txt\n\ttab string\n" +
		"    //go:embed space.txt\n\tspace string\n" +
		"\t//go:embed \"quoted name.txt\" `raw name.txt`\n\tquoted embed.FS\n" +
		")\n\nconst text = \"//go:embed unrelated.md\"\n"
	files := func() map[string]string {
		return map[string]string{
			"go.mod":          "module example.com/m\n\ngo 1.26\n",
			"root.go":         root,
			"tab.txt":         "tab",
			"space.txt":       "space",
			"quoted name.txt": "quoted",
			"raw name.txt":    "raw",
			"unrelated.md":    "named only inside a string literal",
		}
	}
	base := hashModule(t, files())
	for _, path := range []string{"tab.txt", "space.txt", "quoted name.txt", "raw name.txt"} {
		changed := files()
		changed[path] += " changed"
		if hashModule(t, changed) == base {
			t.Errorf("changing embedded %s kept the module hash", path)
		}
	}
	changed := files()
	changed["unrelated.md"] += " changed"
	if hashModule(t, changed) != base {
		t.Error("a //go:embed text inside a string literal selected a file go build does not embed")
	}
}
