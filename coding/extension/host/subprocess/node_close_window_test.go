package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// When the close mechanism is unavailable or throws, the command window still closes once without throwing.
func TestCloseInCloseCallbacksSurvivesAFailingMechanism(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { closeInCloseCallbacks } from %q;
const cases = {
  noPort: () => ({}),
  factoryThrows: () => { throw new Error("no channel"); },
  closeThrows: () => ({ port1: { once() {}, close() { throw new Error("boom"); } } }),
  real: undefined,
};
for (const [name, factory] of Object.entries(cases)) {
  let calls = 0;
  await new Promise((resolve) => { closeInCloseCallbacks(() => { calls++; resolve(); }, factory); });
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(calls, 1, name);
}
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("close window fallback: %v\n%s", err, output)
	}
}
