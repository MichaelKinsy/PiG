package host

// Ports packages/chord/test/boundary.test.ts: Chord does not depend on Pi packages or files outside Chord. The Go
// form: every module package that chord/... links is Chord itself or a product-neutral helper (JSON, numbers, strings,
// paths, the TypeBox value engine and the JSON Schema validator), never another Pi port.

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const module = "github.com/MichaelKinsy/PiG/"

// chordHelpers are the module packages outside chord/ that Chord may link.
var chordHelpers = []string{
	"durable/core/jv", // JavaScript number and JSON string primitives, reached through internal/jsparity
	"extensions/sdk/json",
	"internal/chord/chordjson",
	"internal/entered", // the shared parked-goroutine check behind goEntered
	"internal/goroutineid",
	"internal/jsonscan", // the eight-byte JSON string scan, shared by durable/core/jv and the ai chunk scanner
	"internal/jsnumber",
	"internal/jsparity",
	"internal/jsstring",
	"internal/lazyregexp",
	"internal/nodepath",
	"internal/nodeurl",
	"internal/typebox",
	"third_party/jsonschema",
	"third_party/jsonschema/kind",
}

func TestPackageBoundaryDoesNotDependOnPiPackagesOrFilesOutsideChord(t *testing.T) {
	command := exec.CommandContext(t.Context(), "go", "list", "-deps", "./chord/...")
	command.Dir = testenv.ModuleRoot(t)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		path, ok := strings.CutPrefix(line, module)
		if !ok || path == "chord" || strings.HasPrefix(path, "chord/") || slices.Contains(chordHelpers, path) {
			continue
		}
		violations = append(violations, path)
	}
	if len(violations) > 0 {
		t.Fatalf("chord links %v", violations)
	}
}
