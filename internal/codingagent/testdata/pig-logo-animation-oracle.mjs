// Execute the pinned pi-logo-animation.ts, not a second implementation. Pi's logo is swapped for the PiG pig head (D87)
// by generalizing the logo's 4-by-2-cell geometry to the head's cells, its pixel size in grid units and the clicked logo's
// cells, and by taking the blocks, the radius and, after Pi's puzzle starts, the block offsets and colors from the Go side. Time, timers, the theme and the keybindings are stubbed; no user config.
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";
import * as colors from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/colors.js";
import { visibleWidth } from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/utils.js";

const input = JSON.parse(readFileSync(0, "utf8"));
const path = new URL(`../../../.upstream/v${input.upstream}/packages/coding-agent/src/modes/interactive/components/pi-logo-animation.ts`, import.meta.url);
let source = readFileSync(path, "utf8");
const replaceOnce = (from, to) => {
  const count = source.split(from).length - 1;
  if (count !== 1) throw new Error(`expected one ${JSON.stringify(from)}, found ${count}`);
  source = source.replace(from, to);
};
const blocksStart = source.indexOf("const BLOCKS");
const blocksEnd = source.indexOf("// Farthest distance");
if (blocksStart < 0 || blocksEnd < blocksStart) throw new Error("BLOCKS not found");
source = source.slice(0, blocksStart) + "const BLOCKS = globalThis.input.blocks;\n" + source.slice(blocksEnd);
replaceOnce("Math.hypot(2, 2, 1 + DEPTH / 2)", "globalThis.input.radius");
const unitDots = 2 / input.pixel;
replaceOnce("block.home[0] - 2 + dx", `(block.home[0] + dx) * ${input.pixel} - ${input.centerX}`);
replaceOnce("block.home[1] - 2 + dy", `(block.home[1] + dy) * ${input.pixel} - ${input.centerY}`);
replaceOnce("max: [x + 1, y + 1, dz + DEPTH / 2], color: block.color", `max: [x + ${input.pixel}, y + ${input.pixel}, dz + DEPTH / 2], color: globalThis.colors ? globalThis.colors[index] : block.color`);
replaceOnce("const startScale = (2 * (CAMERA_DISTANCE", `const startScale = (${unitDots} * (CAMERA_DISTANCE`);
replaceOnce("this.options.logoColumn * 2 + 4", `this.options.logoColumn * 2 + ${input.centerX * unitDots}`);
replaceOnce("this.options.logoRow * 4 + 4", `this.options.logoRow * 4 + ${input.centerY * unitDots}`);
replaceOnce("const centerX = this.options.logoColumn + 2;", `const centerX = this.options.logoColumn + ${input.headCells / 2};`);
replaceOnce("const centerY = this.options.logoRow + 1;", `const centerY = this.options.logoRow + ${input.headRows / 2};`);
replaceOnce("row < this.options.logoRow + 2;", `row < this.options.logoRow + ${input.clearRows};`);
replaceOnce("column < this.options.logoColumn + 4;", `column < this.options.logoColumn + ${input.clearColumns};`);
source = source.replace(/^import [\s\S]*?;\n/gm, "").replaceAll("export class", "class").replaceAll("export async function", "async function").replaceAll("export interface", "interface");

let nowMs = 0;
const rgb = ([r, g, b]) => colors.rgbColor(r, g, b);
const keys = { "tui.select.cancel": ["escape"], "app.clear": ["ctrl+c"] };
const keyData = { escape: "\x1b", "ctrl+c": "\x03" };
const context = vm.createContext({
  ...colors, visibleWidth, Intl, Math, Number, Array, Map, Set, Float32Array, Uint8Array, String, input,
  performance: { now: () => nowMs },
  setInterval: () => ({ unref() {} }),
  clearInterval: () => {},
  theme: {
    appearance: input.appearance,
    colors: { text: rgb(input.theme.text), muted: rgb(input.theme.muted), dim: rgb(input.theme.dim) },
    getColorMode: () => input.colorMode,
  },
  formatKeyText: (key) => key,
  getKeybindings: () => ({
    getKeys: (id) => keys[id] ?? [],
    matches: (data, id) => (keys[id] ?? []).some((key) => keyData[key] === data),
  }),
});
vm.runInContext(`${stripTypeScriptTypes(source)}
globalThis.make = (options, colors, onDone) => {
  const animation = new PiLogoAnimation({ terminal: { rows: input.height }, requestRender() {} }, options, colors, onDone);
  const original = animation.blockOffsets.bind(animation);
  animation.blockOffsets = (time) => (globalThis.offsets ? globalThis.offsets : original(time));
  return animation;
};`, context);

let done = 0;
const animation = context.make(
  { screen: input.screen, logoColumn: input.logoColumn, logoRow: input.logoRow },
  { foreground: input.foreground, background: input.background },
  () => { done++; },
);
const output = [];
for (const step of input.steps) {
  nowMs = step.ms;
  context.offsets = step.offsets ?? undefined;
  context.colors = step.colors ?? undefined;
  switch (step.kind) {
    case "render":
      output.push(animation.render(input.width));
      break;
    case "key":
      animation.handleInput(step.data);
      output.push(done);
      break;
    case "click":
      output.push(animation.handleMouse({ type: step.type }));
      output.push(done);
      break;
    case "invalidate":
      animation.invalidate();
      output.push(null);
      break;
    default:
      throw new Error(`unknown step ${step.kind}`);
  }
}
console.log(JSON.stringify(output));
