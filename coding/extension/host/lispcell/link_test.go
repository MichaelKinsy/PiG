package lispcell

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const module = "github.com/MichaelKinsy/PiG"

// lispSymbols are the code the Lisp support adds: the SBCL image build and the Quicklisp resolver of runtimecell, the Lisp staging of the extension host
// and the Lisp source resolver. Each is referenced only from the init of lispcell, through the hooks EnableLisp installs.
var lispSymbols = []string{
	module + "/coding/extension/host/runtimecell.buildLispImage",
	module + "/coding/extension/host/runtimecell.ResolveQuicklisp",
	module + "/coding/extension/host/subprocess.(*Host).stagePackedLisp",
	module + "/coding/extension/host/subprocess.lispImageCommand",
	module + "/coding/extension/source.resolveLisp",
}

func goOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", args...)
	command.Dir = root
	out, err := command.Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

// An embedding program built on the extension host does not import lispcell, so it carries none of the Lisp support; cmd/pig imports it.
func TestOnlyPigLinksLispSupport(t *testing.T) {
	if testing.Short() {
		t.Skip("go list and a link of examples/headless in -short mode")
	}
	root := testenv.ModuleRoot(t)
	lispcell := module + "/coding/extension/host/lispcell"
	if deps := strings.Fields(goOutput(t, root, "list", "-deps", "./examples/headless")); slices.Contains(deps, lispcell) {
		t.Errorf("examples/headless links %s", lispcell)
	}
	if deps := strings.Fields(goOutput(t, root, "list", "-deps", "./cmd/pig")); !slices.Contains(deps, lispcell) {
		t.Errorf("cmd/pig does not link %s", lispcell)
	}
	binary := filepath.Join(t.TempDir(), "headless")
	goOutput(t, root, "build", "-o", binary, "./examples/headless")
	if _, err := os.Stat(binary); err != nil {
		t.Fatal(err)
	}
	symbols := goOutput(t, root, "tool", "nm", binary)
	for _, symbol := range lispSymbols {
		if strings.Contains(symbols, " "+symbol+"\n") {
			t.Errorf("examples/headless links %s", symbol)
		}
	}
}
