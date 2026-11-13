//go:build !pig_strip_node_extensions

package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's getImageLineExtent measures the text before an image sequence with visibleWidth (packages/tui/src/terminal-image.ts, Pi 787005a40). The vendored terminal-image.js loads with every Node extension start, because Theme reads its capability cache for the colour mode, so its import of the width module waits for the first measurement (automation/gen/vendor-pi-startup-seams.mjs, shims/pi-tui-utils-lazy.mjs). The deferral changes no result: every extent equals the one Pi's own module returns, the width module loads once, and the capabilities the host sends before and after an extension's first image reach its rendering as they reach Pi's.
func TestNodeTerminalImageDefersTheWidthModule(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	// The installed Pi the parity tests compare with (make parity-deps): the pinned release with the recorded post-release fixes.
	pi := filepath.Join(moduleRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "@earendil-works", "pi-tui", "dist")
	entry := filepath.Join(t.TempDir(), "images.ts")
	write(t, entry, `import { getCapabilities, renderImage } from "@earendil-works/pi-tui";
export default () => ({
  capabilities: () => getCapabilities(),
  image: () => renderImage("AAAA", { widthPx: 20, heightPx: 20 }, { maxWidthCells: 4, imageId: 7 }),
});`)
	script := `import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";
const root = pathToFileURL(process.argv[1] + "/");
const widthModule = new URL("shims/pi-dist/pi-tui/utils.js", root).href;
let widthLoads = 0;
registerHooks({load(url, context, next) {
  if (url === widthModule) widthLoads++;
  return next(url, context);
}});
const { Runtime } = await import(new URL("runtime.mjs", root));
const { importExtension } = await import(new URL("jiti-loader.mjs", root));
const runtime = new Runtime(process.argv[3]);
const vendored = await import(new URL("shims/pi-dist/pi-tui/terminal-image.js", root));
const pi = await import(pathToFileURL(process.argv[2] + "/terminal-image.js"));
// The host's capabilities as the runtime hands them to pi-tui (applyTerminalCapabilities), given to Pi's own module too.
const send = (caps) => {
  runtime.applyState({ terminalCapabilities: caps });
  pi.setCapabilities({ images: caps.images || null, trueColor: caps.trueColor === true, hyperlinks: caps.hyperlinks === true });
  assert.deepEqual(vendored.getCapabilities(), pi.getCapabilities());
};

// A capability change that arrives before anything measured or rendered an image.
send({ images: "kitty", trueColor: true, hyperlinks: true });
assert.equal(widthLoads, 0, "the width module loaded before an image line was measured");

// Lines without an image sequence need no width.
for (const line of ["", "plain text", "\x1b[31mred\x1b[0m"]) {
  assert.equal(vendored.getImageLineExtent(line), undefined);
  assert.equal(pi.getImageLineExtent(line), undefined);
}
assert.equal(widthLoads, 0, "the width module loaded for a line without an image");

const kitty = vendored.encodeKitty("AAAA", { columns: 4, rows: 2, imageId: 7 });
assert.equal(kitty, pi.encodeKitty("AAAA", { columns: 4, rows: 2, imageId: 7 }));
const iterm2 = vendored.encodeITerm2("AAAA", { width: 4, height: "auto" });
assert.equal(iterm2, pi.encodeITerm2("AAAA", { width: 4, height: "auto" }));
assert.deepEqual(vendored.getImageLineExtent("日本語 " + kitty), { firstRow: 0, lastRow: 1, startCol: 7, endCol: 11 });
assert.equal(widthLoads, 1, "the first measurement loads the width module");
// visibleWidth's cases: nothing, ASCII, wide characters, emoji with a modifier, a combining mark, a tab, SGR and OSC 8 sequences.
const prefixes = ["", "ab", "日本語 ", "👍🏽 ok ", "e\u0301 ", "\ta", "\x1b[31mred\x1b[0m ", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ ", "\x1b[1m太字\x1b[22m👩‍💻"];
for (const prefix of prefixes) {
  for (const line of [prefix + kitty, prefix + iterm2, prefix + "\x1b[2A" + iterm2, prefix + kitty + " after"]) {
    const extent = vendored.getImageLineExtent(line);
    assert.ok(extent, JSON.stringify(line));
    assert.deepEqual(extent, pi.getImageLineExtent(line), JSON.stringify(line));
  }
}
assert.equal(vendored.getImageLineExtent("\x1b[31mred\x1b[0m " + kitty).startCol, 4);
assert.equal(vendored.getImageLineExtent("ab\x1b[2A" + iterm2).firstRow, -2);
assert.equal(widthLoads, 1, "the width module loads once");

// The extension's first image comes after that capability change, with what Pi renders for the same terminal.
const extension = (await importExtension(process.argv[3]))();
const piImage = () => pi.renderImage("AAAA", { widthPx: 20, heightPx: 20 }, { maxWidthCells: 4, imageId: 7 });
assert.deepEqual(extension.capabilities(), { images: "kitty", trueColor: true, hyperlinks: true });
assert.ok(extension.image().sequence.startsWith("\x1b_G"));
assert.deepEqual(extension.image(), piImage());
assert.deepEqual(vendored.getImageLineExtent("日本 " + extension.image().sequence), pi.getImageLineExtent("日本 " + piImage().sequence));
// A capability change after the first image.
send({ images: "iterm2", trueColor: false, hyperlinks: false });
assert.deepEqual(extension.capabilities(), { images: "iterm2", trueColor: false, hyperlinks: false });
assert.ok(extension.image().sequence.startsWith("\x1b]1337;File="));
assert.deepEqual(extension.image(), piImage());
send({ trueColor: true, hyperlinks: true });
assert.deepEqual(extension.capabilities(), { images: null, trueColor: true, hyperlinks: true });
assert.equal(extension.image(), null);
assert.equal(piImage(), null);
send({ images: "kitty", trueColor: true, hyperlinks: false });
assert.deepEqual(extension.image(), piImage());
assert.equal(widthLoads, 1, "the width module loads once");
`
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "--eval", script, root, pi, entry)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal image width deferral: %v\n%s", err, output)
	}
}
