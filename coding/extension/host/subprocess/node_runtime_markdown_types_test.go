//go:build !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi eebd5d90 registerMarkdownTransformer(fn, { messageTypes }): the Node runtime's register frame carries the default surfaces without options, the listed
// ones as given, an empty list as an empty list, and nothing without a transformer.
func TestNodeRuntimeRegisterFrameCarriesTheMarkdownMessageTypes(t *testing.T) {
	nodeCellRequireNode(t)
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { Runtime } = await import(pathToFileURL(%q));
async function frame(register) {
  const rt = new Runtime("types.mjs");
  const sent = [];
  rt.conn = { send: (message) => sent.push(message), loading: false };
  register(rt.api);
  rt.connect().catch(() => {});
  await new Promise(setImmediate);
  const message = sent.find((m) => m.type === "register");
  assert.ok(message, "no register frame");
  return message.register.markdown_message_types;
}
const fn = (markdown) => markdown;
assert.equal(await frame(() => {}), undefined);
assert.deepEqual(await frame((api) => api.registerMarkdownTransformer(fn)), ["user", "assistant", "assistant-thinking"]);
assert.deepEqual(await frame((api) => api.registerMarkdownTransformer(fn, {})), ["user", "assistant", "assistant-thinking"]);
assert.deepEqual(await frame((api) => api.registerMarkdownTransformer(fn, { messageTypes: ["custom", "summary"] })), ["custom", "summary"]);
assert.deepEqual(await frame((api) => api.registerMarkdownTransformer(fn, { messageTypes: [] })), []);
`, runtimePath)
	cmd := exec.CommandContext(testbudget.Context(t), "node", "--input-type=module", "--eval", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("register frame: %v\n%s", err, out)
	}
}
