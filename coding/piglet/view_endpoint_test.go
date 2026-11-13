package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pig additive (D614): viewEndpoint takes none or socket and nothing else; a Piglet without the key serves no socket.
func TestParse_ViewEndpoint(t *testing.T) {
	for source, want := range map[string]bool{
		"name: x\nviewEndpoint: socket\n": true,
		"name: x\nviewEndpoint: none\n":   false,
		"name: x\n":                       false,
	} {
		p, err := ParseBytes([]byte(source))
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		if got := p.ViewEndpointSocket(); got != want {
			t.Errorf("%q: ViewEndpointSocket() = %v, want %v", source, got, want)
		}
	}
	for _, source := range []string{"name: x\nviewEndpoint: tcp\n", "name: x\nviewEndpoint: true\n"} {
		if p, err := ParseBytes([]byte(source)); err == nil {
			t.Errorf("%q accepted as %q", source, p.ViewEndpoint)
		} else if !strings.Contains(err.Error(), "viewEndpoint") {
			t.Errorf("%q: error %q does not name viewEndpoint", source, err)
		}
	}
	var none *Piglet
	if none.ViewEndpointSocket() {
		t.Error("a run with no Piglet serves a view endpoint")
	}
}

// pig additive (D614): a child inherits viewEndpoint, replaces it by naming it, and a clone keeps it.
func TestViewEndpointInheritsAndIsReplaced(t *testing.T) {
	dir := t.TempDir()
	writePigletSource(t, dir, "base", "name: base\nviewEndpoint: socket\n")
	child := writePigletSource(t, dir, "child", "name: child\nextends:\n  source: local:./base.yaml\n")
	effective, err := ResolveEffective(child)
	if err != nil {
		t.Fatal(err)
	}
	if !effective.Piglet.ViewEndpointSocket() {
		t.Error("a child does not inherit viewEndpoint: socket")
	}
	if !Clone(effective.Piglet).ViewEndpointSocket() {
		t.Error("a clone drops viewEndpoint")
	}
	off := writePigletSource(t, dir, "off", "name: off\nextends:\n  source: local:./base.yaml\nviewEndpoint: none\n")
	effective, err = ResolveEffective(off)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Piglet.ViewEndpointSocket() {
		t.Error("viewEndpoint: none in a child did not replace the base's socket")
	}
}

// pig additive (D614): PiG Standard serves no view endpoint, and a baked Piglet keeps the key.
func TestStandardServesNoViewEndpoint(t *testing.T) {
	path := filepath.Join("..", "..", "piglets", "standard", "pig-standard.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if p.ViewEndpointSocket() {
		t.Error("piglets/standard/pig-standard.yaml sets viewEndpoint: socket")
	}
	baked, err := Bake(&Piglet{Name: "x", ViewEndpoint: ViewEndpointSocket}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseBytes(baked)
	if err != nil {
		t.Fatal(err)
	}
	if !back.ViewEndpointSocket() {
		t.Errorf("the baked Piglet lost viewEndpoint:\n%s", baked)
	}
}
