package evalsuites

// pi: packages/evals/evals/documentation-audit.eval.ts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// piAuditParameters is documentation-audit.eval.ts:15-21 as Pi evaluates it: `Type.Object({ verdict: Type.Union([Type.Literal("match"), ...]), explanation: Type.String({ minLength: 1, maxLength: 2000 }) }, { additionalProperties: false })`,
// stringified by the TypeBox of the installed Pi comparator.
func piAuditParameters(t *testing.T) string {
	t.Helper()
	root := os.Getenv("PI_PACKAGE_ROOT")
	if root == "" {
		root = filepath.Join(testenv.ModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	}
	typebox := filepath.Join(root, "node_modules", "typebox", "build", "index.mjs")
	if _, err := os.Stat(typebox); err != nil {
		t.Fatalf("the exact Pi comparator is not installed at %s (run make parity-deps): %v", root, err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	script := `import { pathToFileURL } from "node:url";
const typebox = await import(pathToFileURL(process.argv[1]).href);
const Type = typebox.Type ?? typebox.default;
process.stdout.write(JSON.stringify(Type.Object({
	verdict: Type.Union([Type.Literal("match"), Type.Literal("mismatch"), Type.Literal("inconclusive")]),
	explanation: Type.String({ minLength: 1, maxLength: 2000 }),
}, { additionalProperties: false })));`
	out, err := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script, typebox).Output()
	if err != nil {
		t.Fatalf("Pi's TypeBox: %v", err)
	}
	return string(out)
}

// documentation-audit.eval.ts:15-21 declares the verdict as a union of literals, which TypeBox writes as `anyOf` of `{"type":"string","const":...}`; the object is type, required,
// properties and then the options, and JSON.stringify keeps that order. The Go schema is byte for byte what Pi's TypeBox writes.
func TestSubmitAuditParametersKeepTheirAuthoredOrder(t *testing.T) {
	encoded, err := json.Marshal(submitAudit().Parameters)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"object","required":["verdict","explanation"],"properties":{"verdict":{"anyOf":[{"type":"string","const":"match"},{"type":"string","const":"mismatch"},{"type":"string","const":"inconclusive"}]},"explanation":{"type":"string","minLength":1,"maxLength":2000}},"additionalProperties":false}`
	if string(encoded) != want {
		t.Errorf("submit_documentation_audit parameters\n got: %s\nwant: %s", encoded, want)
	}
	if pi := piAuditParameters(t); string(encoded) != pi {
		t.Errorf("submit_documentation_audit parameters\n  pig: %s\n   pi: %s", encoded, pi)
	}
}
