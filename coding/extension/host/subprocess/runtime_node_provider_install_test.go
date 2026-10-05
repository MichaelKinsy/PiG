package subprocess

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// pi-background-tasks@2.6.9 registers a streamSimple override for the built-in
// anthropic provider from a session_start handler, then reads the registry
// synchronously and requires that the registration replaced the effective
// provider (dist/extensions/anthropic-attribution.js confirmProviderInstallation,
// ISC; the check is reproduced below). Pi's ModelRuntime.registerProvider
// recomposes the provider before it returns (model-runtime.ts:919-940), so
// getProvider, getRegisteredProviderConfig and getRegisteredNativeProvider
// already agree when the call returns. The Host's registry snapshot is older
// than that call, so the registration the process holds must outrank the
// snapshot's "composed" flag.
func TestNodeRegisterProviderRecomposesBuiltinProviderSynchronously(t *testing.T) {
	script := `
import assert from "node:assert/strict";
import { Runtime } from "./runtime-node/runtime.mjs";
const ANTHROPIC_PROVIDER = "anthropic";
function captureProviderSnapshot(registry) {
  return {
    effective: registry.getProvider(ANTHROPIC_PROVIDER),
    legacy: registry.getRegisteredProviderConfig(ANTHROPIC_PROVIDER),
    native: registry.getRegisteredNativeProvider(ANTHROPIC_PROVIDER),
  };
}
function confirmProviderInstallation(registry, before) {
  const token = registry.getRegisteredProviderConfig(ANTHROPIC_PROVIDER);
  const native = registry.getRegisteredNativeProvider(ANTHROPIC_PROVIDER);
  const effective = registry.getProvider(ANTHROPIC_PROVIDER);
  if (token === before.legacy && native === before.native && effective === before.effective) return undefined;
  if (token === undefined || token === before.legacy || native !== undefined || effective === undefined ||
      effective === before.effective || token.streamSimple === before.legacy?.streamSimple) {
    throw new Error("pi_anthropic_attribution_install_failed: host provider registration did not install the package transport atomically");
  }
  return { registry, before, token };
}
const runtime = new Runtime("/ext/anthropic-attribution.mjs");
runtime.applyModelRegistryState({ providers: { anthropic: { name: "Anthropic", composed: false } } });
const registry = runtime.ctx.modelRegistry;
const before = captureProviderSnapshot(registry);
assert.ok(before.effective, "the built-in provider is the effective provider before the registration");
runtime.registerProvider(ANTHROPIC_PROVIDER, { api: "anthropic-messages", streamSimple: () => { throw new Error("unused"); } });
const installation = confirmProviderInstallation(registry, before);
assert.ok(installation, "the registration was not observed");
assert.notStrictEqual(registry.getProvider(ANTHROPIC_PROVIDER), before.effective, "the effective provider is still the built-in one");

// Pi's unregisterProvider recomposes the same way, so the built-in provider is effective again at once (model-runtime.ts:942-948).
registry.unregisterProvider(ANTHROPIC_PROVIDER);
assert.equal(registry.getRegisteredProviderConfig(ANTHROPIC_PROVIDER), undefined);
assert.strictEqual(registry.getProvider(ANTHROPIC_PROVIDER), before.effective, "unregister kept the composed provider");
assert.equal(registry.getRegisteredProviderIds().includes(ANTHROPIC_PROVIDER), false);
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provider install: %v\n%s", err, output)
	}
}

// Pi's unregisterProvider deletes the extension layer and recomposes the
// provider from its built-in and models.json layers before it returns
// (model-runtime.ts:942-948, composeProvider at 300-316). Once the Host's
// snapshot has caught up with a registration it carries that layer as
// "extensionConfig" with "composed": true, and the Host's update for the
// unregister arrives later, so the process must drop the layer itself.
// pi-background-tasks@2.6.9 unregisters this way in session_shutdown.
func TestNodeUnregisterProviderDropsTheSnapshotExtensionLayer(t *testing.T) {
	script := `
import assert from "node:assert/strict";
import { Runtime } from "./runtime-node/runtime.mjs";
const extensionConfig = { api: "anthropic-messages", baseUrl: "https://extension.test" };
for (const modelsConfig of [undefined, { baseUrl: "https://models-json.test" }]) {
  const runtime = new Runtime("/ext/anthropic-attribution.mjs");
  runtime.commitLoad();
  // The factory finished: a registration after it is a host call.
  runtime.providersSent = true;
  runtime.fireAndForget = () => {};
  // The config's cross-process reference needs a live connection, which this rig has none of.
  runtime.providerConfigRef = () => "ref";
  const layers = modelsConfig ? { modelsConfig } : {};
  runtime.applyModelRegistryState({ providers: { anthropic: { name: "Anthropic", composed: modelsConfig !== undefined, ...layers } } });
  const registry = runtime.ctx.modelRegistry;
  const before = registry.getProvider("anthropic");
  registry.registerProvider("anthropic", { ...extensionConfig, streamSimple: () => { throw new Error("unused"); } });
  // The Host's update for the registration.
  runtime.applyModelRegistryState({
    providers: { anthropic: { name: "Anthropic", composed: true, extensionConfig, ...layers } },
    registered: [{ name: "anthropic", config: extensionConfig }],
  });
  assert.notStrictEqual(registry.getProvider("anthropic"), before);
  registry.unregisterProvider("anthropic");
  assert.equal(registry.getRegisteredProviderConfig("anthropic"), undefined);
  assert.equal(registry.getRegisteredProviderIds().includes("anthropic"), false);
  const after = registry.getProvider("anthropic");
  const baseUrls = new Set(after.getModels().map((model) => model.baseUrl));
  assert.equal(baseUrls.has(extensionConfig.baseUrl), false, "unregister kept the extension layer");
  if (modelsConfig) assert.deepEqual([...baseUrls], [modelsConfig.baseUrl], "unregister dropped the models.json layer");
  else assert.strictEqual(after, before, "unregister did not restore the built-in provider");
}
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provider unregister: %v\n%s", err, output)
	}
}

// The same check through a running Host: the registry snapshot the Host pushed
// before session_start has no anthropic registration, and the handler reads the
// registry right after registerProvider (pi-background-tasks issue #140).
func TestNodeSessionStartProviderRegistrationIsVisibleToTheRegistry(t *testing.T) {
	shortSockDir(t)

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getModelRegistryState", func() map[string]any {
		return map[string]any{"providers": map[string]any{"anthropic": map[string]any{"name": "Anthropic", "composed": false}}}
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "provider-install-session-start",
		Source:  filepath.Join("testdata", "provider-install-session-start.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ext.Handlers["session_start"]) == 0 {
		t.Fatal("missing session_start handler")
	}
	if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}); err != nil {
		t.Fatalf("session_start: %v", err)
	}
}

// A provider the snapshot has never seen, with no models yet, is visible as soon
// as registerProvider returns: Pi composes it from the extension layer alone
// (model-runtime.ts:919-940).
func TestNodeRegisterProviderMakesANewProviderVisibleAtOnce(t *testing.T) {
	script := `
import assert from "node:assert/strict";
import { Runtime } from "./runtime-node/runtime.mjs";

const runtime = new Runtime("/ext/new-provider.mjs");
runtime.applyModelRegistryState({ providers: {} });
const registry = runtime.contextValues.modelRegistry;
assert.equal(registry.getProvider("brand-new"), undefined);
runtime.registerProvider("brand-new", { api: "openai-completions", baseUrl: "https://brand-new.test", streamSimple: () => { throw new Error("unused"); } });
const provider = registry.getProvider("brand-new");
assert.ok(provider, "a newly registered provider is not visible to getProvider");
runtime.unregisterProvider("brand-new");
assert.equal(registry.getProvider("brand-new"), undefined, "an unregistered provider is still visible");
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("new provider: %v\n%s", err, output)
	}
}
