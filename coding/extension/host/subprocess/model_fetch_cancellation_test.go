package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi openai-completions.ts classifies callback rejection using options.signal.aborted. A bridge response must not overtake cancellation of the provider that classifies it.
func TestNodeModelCallbackCancellationPrecedesResponse(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { setImmediate } from "node:timers/promises";
import { Runtime } from %q;

for (const api of ["openai-completions", "openai-responses", "anthropic-messages"]) {
  for (const callback of ["fetch", "onPayload", "onResponse", "transformHeaders"]) {
    for (const phase of ["during callback", "before callback", "ordinary error"]) {
      const runtime = new Runtime("/ext/callback-cancellation.mjs");
      const caller = new AbortController();
      const provider = new AbortController();
      const completion = Promise.withResolvers();
      const cancellation = Promise.withResolvers();
      let cancellationRequested = false;
      runtime.call = (method) => {
        if (method === "modelStream") return completion.promise;
        assert.equal(method, "cancelModelStream");
        cancellationRequested = true;
        return cancellation.promise;
      };
      const callbackError = new Error("callback rejected");
      let callbackRejected = false;
      const fn = () => new Promise((_resolve, reject) => {
        const fail = () => { callbackRejected = true; reject(callbackError); };
        caller.signal.addEventListener("abort", fail, { once: true });
        if (caller.signal.aborted || phase === "ordinary error") fail();
      });
      if (phase === "before callback") caller.abort();
      const stream = runtime.startModelStream({ id: "model", provider: "fixture", api }, { messages: [] }, {
        signal: caller.signal, [callback]: fn,
      }, true, true);
      const streamId = [...runtime.modelStreamCallbacks.keys()][0];
      let response;
      runtime.respond = async (id, result, error) => { response = { id, result, error }; };
      const dispatch = runtime.handleRequest("callback-request", { method: "model_stream_callback", args: {
        streamId, callback, value: { id: "request", url: "https://fixture.invalid", method: "POST", headers: {} },
      } }, { signal: provider.signal });
      try {
        if (phase === "during callback") caller.abort();
        await setImmediate();
        assert.equal(callbackRejected, true);
        assert.equal(cancellationRequested, phase !== "ordinary error");
        if (phase !== "ordinary error") {
          // The callback has rejected, but the host call lane has not yet applied cancellation. Do not let its error become a non-aborted provider failure.
          assert.equal(response, undefined, api + "/" + callback + "/" + phase + ": response overtook provider cancellation");
          provider.abort();
          cancellation.resolve();
        }
        await dispatch;
        assert.equal(response.id, "callback-request");
        assert.equal(response.error, callbackError);
      } finally {
        provider.abort();
        cancellation.resolve();
        await dispatch;
        const result = { role: "assistant", content: [], stopReason: phase === "ordinary error" ? "error" : "aborted" };
        stream.push({ type: "error", reason: result.stopReason, error: result });
        completion.resolve();
        assert.equal(await stream.result(), result);
        await setImmediate();
        await setImmediate();
        assert.equal(runtime.modelStreamCallbacks.size, 0);
        assert.equal(runtime.modelStreams.size, 0);
      }
    }
  }
}
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("callback cancellation ordering: %v\n%s", err, output)
	}
}
