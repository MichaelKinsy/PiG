package subprocess

import (
	"os/exec"
	"testing"
)

// packages/coding-agent/test/suite/regressions/7193-event-bus-lifecycle.test.ts:16.
// The transport boundary replaces Pi's in-process Session ownership. Keep an independent bus observer alive across two replacement runtimes and final disposal, then exercise both orderly shutdown and disconnected transport cleanup.
func TestNodeEventBusLifecycle7193(t *testing.T) {
	script := `
import assert from "node:assert/strict";
import { Runtime } from "./runtime-node/runtime.mjs";
for (const disconnect of [false, true]) {
  let extensionCalls = 0, hostCalls = 0, firstApi;
  const observer = new Runtime("host-observer.mjs");
  const offHost = observer.api.events.on("reload:test", () => hostCalls++);
  const load = async () => {
    const runtime = new Runtime("listener.mjs");
    firstApi ??= runtime.api;
    runtime.api.events.on("reload:test", () => extensionCalls++);
    runtime.commitLoad();
    let stop, started;
    const shutdown = new Promise(resolve => { stop = resolve; });
    const ready = new Promise(resolve => { started = resolve; });
    let sentReady = false;
    runtime.connect = async () => { runtime.conn = {
      next: async () => {
        if (!sentReady) { sentReady = true; return {type:"ready", ready:{cwd:process.cwd(), mode:"print"}}; }
        started();
        await shutdown;
        return disconnect ? null : {type:"shutdown"};
      },
    }; };
    const done = runtime.run();
    await ready;
    return {runtime, dispose: async () => { stop(); await done; }};
  };
  const emit = async () => {
    const extensionBefore = extensionCalls, hostBefore = hostCalls;
    observer.api.events.emit("reload:test", undefined);
    await new Promise(setImmediate);
    return {extension:extensionCalls-extensionBefore, host:hostCalls-hostBefore};
  };
  let current = await load();
  try {
    assert.doesNotThrow(() => firstApi.getCommands());
    assert.deepEqual(await emit(), {extension:1, host:1});
    await current.dispose();
    current = await load();
    assert.throws(() => firstApi.getCommands(), /stale after session replacement or reload/);
    assert.deepEqual(await emit(), {extension:1, host:1});
    await current.dispose();
    current = await load();
    assert.deepEqual(await emit(), {extension:1, host:1});
    await current.dispose();
    assert.deepEqual(await emit(), {extension:0, host:1});
  } finally {
    await current.dispose();
    offHost();
  }
}
`
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("event-bus lifecycle: %v\n%s", err, output)
	}
}
