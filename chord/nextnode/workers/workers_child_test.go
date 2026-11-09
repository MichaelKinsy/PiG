package workers

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// pi: packages/chord/src/node/workers/child.ts
// Ports packages/chord/test/node/workers/child.test.ts ("the child entry's imports › loads no esbuild"): a forked worker does not
// pay for the server's build tool. Pi checks it on the Node bundle of the child entry; a Go worker is a program that calls
// WorkerMain, so the check is on what that program links: the packages the worker side imports (`go list -deps`).

// linkedEsbuild is the esbuild packages the package pattern links, as `go list -deps` names them.
func linkedEsbuild(t *testing.T, pattern string) []string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go tool to list dependencies: %v", err)
	}
	out, err := exec.Command(goTool, "list", "-deps", "-f", "{{.ImportPath}}", pattern).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pattern, err)
	}
	var linked []string
	for path := range strings.FieldsSeq(string(out)) {
		if strings.HasPrefix(path, "github.com/evanw/esbuild") {
			linked = append(linked, path)
		}
	}
	return linked
}

func TestTheWorkersPackageLinksNoEsbuild(t *testing.T) {
	// The worker's side (WorkerMain, the process lock, the plugin runtime and the Node host its plugins run on) and the server's
	// side of the same package (the component that starts workers, which ships built scripts and builds none).
	for _, pattern := range []string{
		"github.com/MichaelKinsy/PiG/chord/nextnode/workers",
		"github.com/MichaelKinsy/PiG/chord/nextnode/plugins",
		"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model",
	} {
		if linked := linkedEsbuild(t, pattern); len(linked) != 0 {
			t.Errorf("%s links %d esbuild packages (a worker evaluates what the server built and must not link the builder, plugins/build), the first %s", pattern, len(linked), linked[0])
		}
	}
}

// The check sees esbuild where it is linked: the builder links it, so a worker that imported the builder would fail the test above.
func TestTheDependencyCheckSeesTheEsbuildTheBuilderLinks(t *testing.T) {
	linked := linkedEsbuild(t, "github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build")
	if !slices.Contains(linked, "github.com/evanw/esbuild/pkg/api") {
		t.Errorf("the builder links %v, want esbuild's pkg/api among them", linked)
	}
}
