// Run from the repository root: node parity/testdata/next-evidence-app-b/oracle.mjs
// Load the pinned source with fake clipboard I/O, fake timers, and deterministic theme colors.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire, registerHooks } from "node:module";
import { resolve } from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";

const root = process.cwd();
const upstream = resolve(root, ".upstream/current");
const compiler = createRequire(resolve(root, "parity/interface-extractor/package.json"))("typescript");
const dependencies = createRequire(resolve(root, "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/package.json"));
const sourceURL = (path) => pathToFileURL(resolve(upstream, path)).href;
const tui = "packages/tui/src/";
const utils = "packages/coding-agent/src/utils/";
const components = "packages/coding-agent/src/modes/interactive/components/";
const colors = { border: "\x1b[90m", accent: "\x1b[36m", muted: "\x1b[90m" };
const color = (role, text) => colors[role] + text + "\x1b[39m";
globalThis.appBEvidence = {
  command: async () => undefined,
  getNativeClipboard: () => undefined,
  photon: dependencies("@silvia-odwyer/photon-node"),
};
const mock = (source) => ({ url: "data:text/javascript," + encodeURIComponent(source), shortCircuit: true });
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === "@earendil-works/pi-tui") return mock(`
      export { Container } from ${JSON.stringify(sourceURL(tui + "tui.ts"))};
      export { SelectList } from ${JSON.stringify(sourceURL(tui + "components/select-list.ts"))};
      export const getNativeClipboard = () => globalThis.appBEvidence.getNativeClipboard();
    `);
    if (specifier.endsWith("/theme/theme.ts")) return mock(`
      const colors = ${JSON.stringify(colors)};
      export const theme = { fg: (role, text) => colors[role] + text + "\\x1b[39m" };
      export const getSelectListTheme = () => ({
        selectedPrefix: t => theme.fg("accent", t), selectedText: t => theme.fg("accent", t),
        description: t => theme.fg("muted", t), scrollInfo: t => t, noMatch: t => t
      });
    `);
    if (specifier === "./clipboard-command.ts") return mock("export const runClipboardCommand = (...args) => globalThis.appBEvidence.command(...args);");
    if (specifier === "./photon.ts") return mock("export const loadPhoton = async () => globalThis.appBEvidence.photon;");
    if (!specifier.startsWith(".") && !specifier.startsWith("/") && !specifier.includes(":")) {
      return nextResolve(specifier, { ...context, parentURL: pathToFileURL(resolve(root, "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/package.json")).href });
    }
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (url.startsWith("file:") && url.endsWith(".ts")) {
      const source = readFileSync(fileURLToPath(url), "utf8");
      return { format: "module", source: compiler.transpileModule(source, {
        compilerOptions: { target: compiler.ScriptTarget.ESNext, module: compiler.ModuleKind.ESNext },
      }).outputText, shortCircuit: true };
    }
    return nextLoad(url, context);
  },
});

const { ShowImagesSelectorComponent } = await import(sourceURL(components + "show-images-selector.ts"));
for (const current of [true, false]) {
  const selected = [];
  let cancelled = 0;
  const selector = new ShowImagesSelectorComponent(current, value => selected.push(value), () => cancelled++);
  for (const width of [20, 80]) {
    const prefix = current ? ["→ ", "  "] : ["  ", "→ "];
    const labels = ["Yes", "No"];
    const desc = ["Show images inline in terminal", "Show text placeholder instead"];
    const rows = labels.map((label, i) => {
      const spacing = " ".repeat(12 - label.length);
      if ((i === 0) === current) return color("accent", prefix[i] + label + (width > 40 ? spacing + desc[i] : ""));
      return prefix[i] + label + (width > 40 ? color("muted", spacing + desc[i]) : "");
    });
    const want = [color("border", "─".repeat(width)), ...rows, color("border", "─".repeat(width))];
    assert.deepEqual(selector.render(width), want);
    console.log(JSON.stringify({ selector: { current, width, lines: want } }));
  }
  selector.getSelectList().handleInput("\x1b[B");
  selector.getSelectList().handleInput("\r");
  assert.deepEqual(selected, [!current]);
  selector.getSelectList().handleInput("\x1b");
  assert.equal(cancelled, 1);
  assert.deepEqual(selected, [!current]);
  console.log(JSON.stringify({ selector: { current, selected, cancelled } }));
}

const { readClipboardImage } = await import(sourceURL(utils + "clipboard-image.ts"));
const bmp = Buffer.alloc(58);
bmp.write("BM"); bmp.writeUInt32LE(58, 2); bmp.writeUInt32LE(54, 10); bmp.writeUInt32LE(40, 14);
bmp.writeInt32LE(1, 18); bmp.writeInt32LE(1, 22); bmp.writeUInt16LE(1, 26); bmp.writeUInt16LE(24, 28);
bmp.writeUInt32LE(4, 34); bmp[56] = 255;
for (const data of [bmp, Buffer.from("corrupt BMP")]) {
  globalThis.appBEvidence.command = async (name, args) => {
    assert.equal(name, "wl-paste");
    return args.includes("--list-types") ? Buffer.from("image/bmp\n") : data;
  };
  const got = await readClipboardImage({ platform: "linux", env: { WAYLAND_DISPLAY: "1", DISPLAY: ":0" } });
  if (data === bmp) {
    assert.equal(got.mimeType, "image/png");
    assert.deepEqual([...got.bytes.slice(0, 4)], [137, 80, 78, 71]);
  } else assert.equal(got, null);
  console.log(JSON.stringify({ clipboard: { input: data === bmp ? "BMP" : "corrupt BMP", mime: got?.mimeType ?? null } }));
}

const { AltScreenFlashContainer } = await import(sourceURL(tui + "components/alt-screen-flash.ts"));
const realTimeout = globalThis.setTimeout;
const realClear = globalThis.clearTimeout;
let now = 0;
const timers = [];
globalThis.setTimeout = (fn, duration) => {
  const timer = { fn, at: now + duration, unref() {}, active: true };
  timers.push(timer); return timer;
};
globalThis.clearTimeout = timer => { timer.active = false; };
const advance = duration => {
  now += duration;
  for (const timer of timers) if (timer.active && timer.at <= now) { timer.active = false; timer.fn(); }
};
try {
  let renders = 0;
  const flashes = new AltScreenFlashContainer(() => renders++);
  flashes.flash("first"); flashes.flash("second", 250);
  assert.deepEqual(flashes.render(5), ["\x1b[7m firs\x1b[0m\x1b[27m", "\x1b[7m seco\x1b[0m\x1b[27m"]);
  advance(249); assert.equal(renders, 2);
  advance(1); assert.equal(renders, 3);
  advance(749); assert.equal(flashes.render(80).length, 1);
  advance(1); assert.equal(renders, 4); assert.deepEqual(flashes.render(80), []);
  for (const duration of [0, -25]) {
    flashes.flash("brief", duration); advance(0); assert.deepEqual(flashes.render(80), []);
  }
  flashes.flash("disposed", 10); flashes.dispose(); advance(1000);
  assert.equal(renders, 9);
  console.log(JSON.stringify({ flash: { renders, expired: flashes.render(80) } }));
  for (const message of ["", "abcdef", "界🙂e\u0301", "\x1b[31mred\x1b[39m"]) {
    flashes.flash(message);
    for (const width of [0, 1, 4, 5, 80]) console.log(JSON.stringify({ flashRender: { message, width, lines: flashes.render(width) } }));
    flashes.dispose();
  }
} finally { globalThis.setTimeout = realTimeout; globalThis.clearTimeout = realClear; }
const { convertToPng } = await import(sourceURL(utils + "image-convert.ts"));
const imageTests = readFileSync(resolve(upstream, "packages/coding-agent/test/image-processing.test.ts"), "utf8");
const fixture = name => {
  const match = imageTests.match(new RegExp(`const ${name} =\\s*"([^"]+)";`));
  assert.ok(match, `missing upstream fixture ${name}`);
  return match[1];
};
const originalPNG = fixture("TINY_PNG");
assert.deepEqual(await convertToPng(originalPNG, "image/png"), { data: originalPNG, mimeType: "image/png" });
const segment = payload => {
  const out = Buffer.alloc(payload.length + 4);
  out[0] = 255; out[1] = 225; out.writeUInt16BE(payload.length + 2, 2); out.set(payload, 4); return out;
};
const jpeg = Buffer.from(fixture("TINY_JPEG_2X1"), "base64");
const oriented = Buffer.concat([
  jpeg.subarray(0, 2),
  segment(Buffer.from('http://ns.adobe.com/xap/1.0/\0<x:xmpmeta xmlns:x="adobe:ns:meta/"/>')),
  segment(Buffer.concat([Buffer.from("Exif\0\0"), Buffer.from("49492a0008000000010012010300010000000600000000000000", "hex")])),
  jpeg.subarray(2),
]);
for (const [name, data, dimensions] of [["JPEG", fixture("TINY_JPEG"), [2, 2]], ["EXIF after XMP", oriented.toString("base64"), [1, 2]]]) {
  const converted = await convertToPng(data, "image/jpeg");
  assert.equal(converted.mimeType, "image/png");
  const png = Buffer.from(converted.data, "base64");
  assert.deepEqual([png.readUInt32BE(16), png.readUInt32BE(20)], dimensions);
  console.log(JSON.stringify({ imageConvert: { name, dimensions } }));
}
assert.equal(await convertToPng(Buffer.from("not an image").toString("base64"), "image/jpeg"), null);
console.log("PASS: pinned source selector, clipboard conversion, image conversion, and flash probes");
