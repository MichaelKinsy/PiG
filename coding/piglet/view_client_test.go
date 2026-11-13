package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pig additive (D614): viewClient takes none or terminal and nothing else; a Piglet without the key attaches no client.
func TestParse_ViewClient(t *testing.T) {
	for source, want := range map[string]bool{
		"name: x\nviewClient: terminal\n": true,
		"name: x\nviewClient: none\n":     false,
		"name: x\n":                       false,
	} {
		p, err := ParseBytes([]byte(source))
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		if got := p.TerminalViewClient(); got != want {
			t.Errorf("%q: TerminalViewClient() = %v, want %v", source, got, want)
		}
	}
	for _, source := range []string{"name: x\nviewClient: web\n", "name: x\nviewClient: true\n"} {
		if p, err := ParseBytes([]byte(source)); err == nil {
			t.Errorf("%q accepted as %q", source, p.ViewClient)
		} else if !strings.Contains(err.Error(), "viewClient") {
			t.Errorf("%q: error %q does not name viewClient", source, err)
		}
	}
	var none *Piglet
	if none.TerminalViewClient() {
		t.Error("a run with no Piglet attaches the terminal view client")
	}
}

// pig additive (D614): a child inherits viewClient, replaces it by naming it, and a clone keeps it.
func TestViewClientInheritsAndIsReplaced(t *testing.T) {
	dir := t.TempDir()
	writePigletSource(t, dir, "base", "name: base\nviewClient: terminal\n")
	child := writePigletSource(t, dir, "child", "name: child\nextends:\n  source: local:./base.yaml\n")
	effective, err := ResolveEffective(child)
	if err != nil {
		t.Fatal(err)
	}
	if !effective.Piglet.TerminalViewClient() {
		t.Error("a child does not inherit viewClient: terminal")
	}
	if !Clone(effective.Piglet).TerminalViewClient() {
		t.Error("a clone drops viewClient")
	}
	off := writePigletSource(t, dir, "off", "name: off\nextends:\n  source: local:./base.yaml\nviewClient: none\n")
	effective, err = ResolveEffective(off)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Piglet.TerminalViewClient() {
		t.Error("viewClient: none in a child did not replace the base's terminal")
	}
}

// pig additive (D614): PiG Standard selects the terminal view client, and a baked Piglet keeps the key.
func TestStandardSelectsTheTerminalViewClient(t *testing.T) {
	path := filepath.Join("..", "..", "piglets", "standard", "pig-standard.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if !p.TerminalViewClient() {
		t.Error("piglets/standard/pig-standard.yaml does not set viewClient: terminal")
	}
	baked, err := Bake(&Piglet{Name: "x", ViewClient: ViewClientTerminal}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseBytes(baked)
	if err != nil {
		t.Fatal(err)
	}
	if !back.TerminalViewClient() {
		t.Errorf("the baked Piglet lost viewClient:\n%s", baked)
	}
}
