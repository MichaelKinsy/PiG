//go:build !race

package tools

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Admitting a tool call reads the tool's argument schema and validates the arguments against it. The schema is
// encoded once per tool, so admission costs the arguments' decode and one copy of the schema bytes; on a 200 KB write
// call the base allocated 1,754,308 bytes in validation alone (8.6x the arguments) plus the schema copy and encode.
func TestWriteCallAdmissionAllocatesAboutTheDecodedArguments(t *testing.T) {
	tool := &WriteTool{}
	args := json.RawMessage(`{"path":"out.txt","content":"` + strings.Repeat("line of text\\n", (200<<10)/14) + `"}`)
	admit := func() {
		if _, err := ai.ValidateToolArgumentsJSON("write", tool.ArgumentSchema(), args); err != nil {
			t.Fatal(err)
		}
	}
	admit()
	const runs = 5
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		admit()
	}
	runtime.ReadMemStats(&after)
	bytes := (after.TotalAlloc - before.TotalAlloc) / runs
	mallocs := (after.Mallocs - before.Mallocs) / runs
	t.Logf("%d-byte write call: %d bytes, %d allocations per admission", len(args), bytes, mallocs)
	if bytes > uint64(len(args))*4 {
		t.Fatalf("admission allocates %d bytes, want at most 4x the %d-byte arguments", bytes, len(args))
	}
	if n := testing.AllocsPerRun(10, func() { _ = tool.ArgumentSchema() }); n > 1 {
		t.Fatalf("ArgumentSchema allocates %.0f times per call, want only its copy of the encoded bytes", n)
	}
}
