// Execute the pinned easter-egg-3d.ts, not a second implementation, for the 3D pig of /arminsayshi and /pigsayhi.
// pig divergence (D87): Armin's bitmap in the theme's accent color is replaced by the Go side's model: the active
// sprite's head in its colors. Everything else (timeline, sliding puzzle, ray caster, dust, starfield, hint, exit) is the
// pinned source. Time, timers, the theme and the keybindings are stubbed; no user config.
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";
import * as colors from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/colors.js";
import { visibleWidth } from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/utils.js";

const input = JSON.parse(readFileSync(0, "utf8"));
let source = readFileSync(new URL(`../../../.upstream/v${input.upstream}/packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts`, import.meta.url), "utf8");
const replaceOnce = (from, to) => {
  const count = source.split(from).length - 1;
  if (count !== 1) throw new Error(`expected one ${JSON.stringify(from)}, found ${count}`);
  source = source.replace(from, to);
};
replaceOnce("(column, row) => (isArminPixel(column, row) ? color : undefined)", "(column, row) => globalThis.input.pixels[row][column] ?? undefined");
source = source.replace(/^import [\s\S]*?;\n/gm, "").replaceAll("export class", "class").replaceAll("export async function", "async function").replaceAll("export type", "type");
source = `const ARMIN_WIDTH = ${input.columns};\nconst ARMIN_HEIGHT = ${input.rows};\n${source}`;

let nowMs = 0;
const rgb = ([r, g, b]) => colors.rgbColor(r, g, b);
const keys = { "tui.select.cancel": ["escape"], "app.clear": ["ctrl+c"] };
const keyData = { escape: "\x1b", "ctrl+c": "\x03" };
const context = vm.createContext({
  ...colors, visibleWidth, Intl, Math, Number, Array, Map, Set, Float32Array, Uint8Array, Uint16Array, String, input,
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
globalThis.make = (screen, colors, onDone) =>
  new EasterEgg3dAnimation({ terminal: { rows: input.height }, requestRender() {} }, screen, arminModel(undefined), colors, onDone);`, context);

let done = 0;
const animation = context.make(input.screen, { foreground: input.foreground, background: input.background }, () => { done++; });
const output = [];
for (const step of input.steps) {
  nowMs = step.ms;
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
