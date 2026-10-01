package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's ExtensionContext guards every member with runner.assertActive(): the getters (ui, mode, hasUI, cwd, sessionManager, modelRegistry, model, scopedModels, thinkingLevel, signal) and the methods (isIdle, isProjectTrusted, abort, hasPendingMessages, shutdown, getContextUsage, compact, getSystemPrompt) in runner.ts:868-960, the command context's methods in runner.ts:981-1016.
// A guarded member throws the runner's stale message synchronously, also when read through a per-request context, which forwards to the live one (runner.ts:982-988).
func TestNodeContextMembersThrowOnceTheRuntimeIsStale(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime, requestSignal } from %q;
const runtime = new Runtime("/ext/stale.mjs");
const request = () => {
  const requestCtx = Object.create(Object.getPrototypeOf(runtime.ctx));
  for (const key of Object.keys(runtime.ctx)) {
    const descriptor = Object.getOwnPropertyDescriptor(runtime.ctx, key);
    Object.defineProperty(requestCtx, key, { enumerable: true, configurable: true, ...(typeof descriptor.value === "function" ? { value: descriptor.value, writable: true } : { get: () => runtime.ctx[key] }) });
  }
  Object.defineProperty(requestCtx, requestSignal, { value: new AbortController().signal });
  return requestCtx;
};
const getters = ["ui", "mode", "hasUI", "cwd", "sessionManager", "modelRegistry", "model", "scopedModels", "thinkingLevel", "signal"];
const methods = ["isIdle", "isProjectTrusted", "abort", "hasPendingMessages", "shutdown", "getContextUsage", "compact", "getSystemPrompt",
  "getSystemPromptOptions", "waitForIdle", "newSession", "fork", "navigateTree", "switchSession", "reload"];
const contexts = { base: runtime.ctx, request: request() };

// An active runtime answers every member.
for (const [name, ctx] of Object.entries(contexts)) {
  for (const getter of getters) assert.doesNotThrow(() => ctx[getter], name + "." + getter);
}

const stale = "This extension ctx is stale after session replacement or reload.";
runtime.extensionRuntime.invalidate(stale);
const failures = [];
for (const [name, ctx] of Object.entries(contexts)) {
  for (const getter of getters) {
    try { ctx[getter]; failures.push(name + "." + getter + " did not throw"); } catch (error) { if (error.message !== stale) failures.push(name + "." + getter + " threw " + error.message); }
  }
  for (const method of methods) {
    try { const value = ctx[method](); if (value?.catch) value.catch(() => {}); failures.push(name + "." + method + "() did not throw synchronously"); } catch (error) { if (error.message !== stale) failures.push(name + "." + method + "() threw " + error.message); }
  }
}
assert.deepEqual(failures, []);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("stale ctx members: %v\n%s", err, output)
	}
}
