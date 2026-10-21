package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's ModelRegistry keeps its ModelRuntime in the private `runtime` field (model-registry.ts:48-52), and createAgentSession's
// `modelRuntime` option takes it: an extension that starts a nested Session passes ctx.modelRegistry.runtime. The nested AgentSession
// asks it hasConfiguredAuth(provider), isUsingOAuth(provider), getModel and getAuth (agent-session.ts:1651-1655, 223-224).
// The Node runtime's `runtime` was the extension-host object, which has none of them, so the nested prompt failed with
// "this._modelRuntime.hasConfiguredAuth is not a function".
func TestNodeModelRegistryRuntimeAnswersAsAModelRuntime(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
const runtime = new Runtime("/ext/model-runtime.mjs");
const model = (provider, id) => ({ provider, id, name: id, api: "openai-completions", baseUrl: "http://unused.invalid", reasoning: false, input: ["text"], contextWindow: 1000, maxTokens: 100, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } });
runtime.applyModelRegistryState({
  models: [model("keyed", "one"), model("oauthed", "two"), model("bare", "three")],
  providers: {
    keyed: { configured: true, usingOAuth: false, name: "Keyed" },
    oauthed: { configured: true, usingOAuth: true, name: "Oauthed" },
    bare: { configured: false, usingOAuth: false, name: "Bare" },
  },
  registered: [],
});
const registry = runtime.ctx.modelRegistry;
const modelRuntime = registry.runtime;
assert.equal(registry.runtime, modelRuntime, "the registry hands out one ModelRuntime");
for (const method of ["hasConfiguredAuth", "isUsingOAuth", "getModel", "getModels", "getPhysicalModel", "getAvailableSnapshot", "getAuth", "checkAuth", "streamSimple", "registerProvider", "resolveModel"]) {
  assert.equal(typeof modelRuntime[method], "function", method);
}
// model-runtime.ts:543-553: the provider's configured and OAuth state, by provider id.
assert.equal(modelRuntime.hasConfiguredAuth("keyed"), true);
assert.equal(modelRuntime.hasConfiguredAuth("bare"), false);
assert.equal(modelRuntime.hasConfiguredAuth("unknown"), false);
assert.equal(modelRuntime.isUsingOAuth("oauthed"), true);
assert.equal(modelRuntime.isUsingOAuth("keyed"), false);
// model-runtime.ts:441-455,759-762: catalog lookups, with the provider filter of getModels.
assert.equal(modelRuntime.getModel("keyed", "one").id, "one");
assert.equal(modelRuntime.getModel("keyed", "missing"), undefined);
assert.deepEqual(modelRuntime.getModels("oauthed").map((m) => m.id), ["two"]);
assert.deepEqual(modelRuntime.getModels().map((m) => m.id), ["one", "two", "three"]);
assert.equal(modelRuntime.getPhysicalModel("bare", "three").id, "three");
assert.deepEqual(modelRuntime.getAvailableSnapshot().map((m) => m.id), ["one", "two"]);
// model-registry.ts keeps its public facade beside it.
assert.equal(registry.hasConfiguredAuth(model("keyed", "one")), modelRuntime.hasConfiguredAuth("keyed"));
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("ModelRuntime of ctx.modelRegistry: %v\n%s", err, output)
	}
}
