//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Ports packages/coding-agent/test/suite/regressions/11156-pi-ai-models-virtual-module.test.ts. An extension that imports
// @earendil-works/pi-ai/models (for clampThinkingLevel, say) failed with "Cannot find module" because the subpath was missing from
// the loader's virtual modules (core/extensions/virtual-modules.ts:29,37). Pi's test reads VIRTUAL_MODULES; this one loads an
// extension through the runtime's jiti loader and a native import through its hooks, since the shim table is what PiG serves.
func TestNodeExtensionImportsPiAiModelsSubpath(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	entry := filepath.Join(t.TempDir(), "models-ext.ts")
	if err := os.WriteFile(entry, []byte(`import { clampThinkingLevel } from "@earendil-works/pi-ai/models";
import { clampThinkingLevel as legacyClamp } from "@mariozechner/pi-ai/models";
import * as models from "@earendil-works/pi-ai/models";
import * as legacyModels from "@mariozechner/pi-ai/models";
import { clampThinkingLevel as rootClamp } from "@earendil-works/pi-ai";
export default () => ({ clampThinkingLevel, legacyClamp, models, legacyModels, rootClamp });
`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const root = pathToFileURL(process.argv[1] + "/");
const { importExtension } = await import(new URL("jiti-loader.mjs", root));
const imported = (await importExtension(process.argv[2]))();
assert.equal(typeof imported.clampThinkingLevel, "function");
// A model without reasoning supports only "off", so every requested level clamps to it.
const model = { id: "m", name: "m", api: "openai-completions", provider: "p", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
assert.equal(imported.clampThinkingLevel(model, "high"), "off");
assert.equal(imported.legacyClamp, imported.clampThinkingLevel);
// The subpath shares the AI core with the root entry instead of loading a second copy.
assert.equal(imported.rootClamp, imported.clampThinkingLevel);
assert.deepEqual(Object.keys(imported.legacyModels), Object.keys(imported.models));
for (const key of Object.keys(imported.models)) assert.equal(imported.legacyModels[key], imported.models[key]);
// A native import resolves to the same namespace under both scopes.
const current = await import("@earendil-works/pi-ai/models");
const legacy = await import("@mariozechner/pi-ai/models");
assert.equal(current, legacy);
assert.equal(current.clampThinkingLevel, imported.clampThinkingLevel);
`
	cmd := exec.Command(node, "--import", registerLoaderURL(t, root), "--input-type=module", "--eval", script, root, entry)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pi-ai/models virtual module: %v\n%s", err, output)
	}
}
