//go:build !pig_strip_node_extensions

package subprocess

// pi: packages/coding-agent/src/core/extensions/pi-ai-extension-view.ts

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The module Node extensions import as `@earendil-works/pi-ai` is Pi's extension view: pi-ai's compat entry plus TypeBox's Type and a TypeBox StringEnum. The pinned Pi's own
// compiled view (dist/core/extensions/pi-ai-extension-view.js) is the oracle: the shim must export the same names, make StringEnum return the same TypeBox value, with the same
// falsy-option rules and members in the same order, and serve the one Type that `typebox` serves.
func TestNodePiAiExtensionViewMatchesThePinnedView(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	script := `import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const view = await import("@earendil-works/pi-ai");
const legacy = await import("@mariozechner/pi-ai");
const typebox = await import("typebox");
const oracle = await import(pathToFileURL(process.argv[1]));
assert.equal(view, legacy, "both pi-ai specifiers serve one module");
assert.deepEqual(Object.keys(view).sort(), Object.keys(oracle).sort(), "export inventory");
assert.equal(view.Type, typebox.Type, "the view's Type is the one typebox serves");
assert.deepEqual(Object.keys(view.Type), Object.keys(oracle.Type), "Type members");
assert.equal(typeof view.StringEnum, "function");
assert.equal(view.StringEnum.length, oracle.StringEnum.length);

const shape = (schema) => ({
  json: JSON.stringify(schema),
  own: Object.getOwnPropertyNames(schema).map((name) => [name, Object.getOwnPropertyDescriptor(schema, name).enumerable]),
  unsafe: view.Type.IsUnsafe(schema),
});
const oracleShape = (schema) => ({
  json: JSON.stringify(schema),
  own: Object.getOwnPropertyNames(schema).map((name) => [name, Object.getOwnPropertyDescriptor(schema, name).enumerable]),
  unsafe: oracle.Type.IsUnsafe(schema),
});
const calls = [
  [["a", "b"]],
  [["a", "b"], undefined],
  [["a", "b"], null],
  [["a", "b"], {}],
  [["a", "b"], { description: "d" }],
  [["a", "b"], { default: "a" }],
  [["a", "b"], { description: "d", default: "b" }],
  [["a", "b"], { description: "", default: "" }],
  [["a", "b"], { description: 0, default: 0 }],
  [["a", "b"], { description: false, default: false }],
  [["a", "b"], { description: undefined, default: undefined }],
  [["a", "b"], { description: "d", default: "a", title: "ignored", extra: 1 }],
  [["a", "b"], { default: "z" }],
  [[], { description: "empty" }],
  [["only"]],
  [["é", "日本", "\u2028", "a\"b"], { description: "unicode \u2029 \"q\"" }],
];
for (const args of calls) {
  assert.deepEqual(shape(view.StringEnum(...args)), oracleShape(oracle.StringEnum(...args)), "StringEnum(" + JSON.stringify(args) + ")");
}
const values = ["x", "y"];
assert.equal(view.StringEnum(values).enum === values, oracle.StringEnum(values).enum === values, "the schema holds the caller's array as Pi's does");
assert.equal(
  JSON.stringify(view.Type.Object({ mode: view.StringEnum(["a", "b"], { description: "m" }), n: view.Type.Optional(view.StringEnum(["c"])) })),
  JSON.stringify(oracle.Type.Object({ mode: oracle.StringEnum(["a", "b"], { description: "m" }), n: oracle.Type.Optional(oracle.StringEnum(["c"])) })),
  "StringEnum inside Type.Object",
);
`
	agent := filepath.Join(findModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	oracle := filepath.Join(agent, "dist", "core", "extensions", "pi-ai-extension-view.js")
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "-e", script, oracle)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extension view: %v\n%s", err, output)
	}
}
