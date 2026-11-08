package host

// Ports packages/chord/test/boundary.test.ts: Chord does not depend on Pi packages other than pi-telemetry and pi-schema,
// or on files outside Chord. The Go form: every module package that chord/... links is Chord itself, telemetry
// (pi-telemetry: a leaf without dependencies, needed for Context.telemetry), schema (pi-schema, for schemas: decision "A
// schema package beneath pi-ai and Chord") or a product-neutral helper (JSON, numbers, strings, paths and the JSON Schema
// validator), never another Pi port.

import (
	"os"
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
	"internal/chord/winpipe", // the Windows named-pipe listener of the plugins' Node subprocess and of a worker's channel and lock; it has code only on Windows
	"internal/entered",       // the shared parked-goroutine check behind goEntered
	"internal/goroutineid",
	"internal/jsclock", // Date.now() and performance.now() with a replaceable source, read by the telemetry recorders
	"internal/jsnumber",
	"internal/jsonparse", // JSON.parse's SyntaxError text, read by the store's outbox (chord/next/store)
	"internal/jsparity",
	"internal/jsstring",
	"internal/lazyregexp",
	"internal/linkerexec", // starts Node for the plugins worker and checks its version (chord/nextnode/plugins)
	"internal/nodepath",
	"internal/nodeurl",
	"internal/sortx",            // sorts short slices with one shared copy of the sort algorithm: a stdlib-only leaf, read by internal/jsstring, schema/internal/ecmaregex and chord/delta
	"internal/watchentered",     // tells a watch delivery that its listener went on to a wait of the host (chord/watch_impl.go)
	"schema",                    // pi-schema, the schema builders and validators of contracts and frames
	"schema/internal/ecmaregex", // pi-schema's ECMAScript regular expressions
	"telemetry",                 // pi-telemetry, the dependency of Context.telemetry (chord/context)
}

// The dependencies differ by platform (the Windows channels link internal/chord/winpipe), so each shipped platform is listed, not only the host's.
func TestPackageBoundaryDoesNotDependOnPiPackagesOrFilesOutsideChord(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "go", "list", "-deps", "./chord/...")
			command.Dir = testenv.ModuleRoot(t)
			command.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
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
		})
	}
}
