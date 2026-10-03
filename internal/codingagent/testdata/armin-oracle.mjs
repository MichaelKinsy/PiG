// Execute the pinned source, not a second implementation. No real timers or user config.
// pig divergence (D87): PiG draws a pig head labeled "pigsayhi", so the Go test passes its image as XBM bits (LSB first,
// 0 is foreground, as armin.ts reads them) and its label, and they replace Armin's in the pinned source.
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";
import { createHash } from "node:crypto";

const image = JSON.parse(process.argv[2]);
let source = readFileSync(new URL(`../../../.upstream/v${image.upstream}/packages/coding-agent/src/modes/interactive/components/armin.ts`, import.meta.url), "utf8")
  .replace(/^import .*;\n/gm, "")
  .replace("export class ArminComponent", "class ArminComponent");
const replaceOnce = (pattern, replacement) => {
  const matches = source.match(new RegExp(pattern.source, "g")) ?? [];
  if (matches.length !== 1) throw new Error(`expected one ${pattern}, found ${matches.length}`);
  source = source.replace(pattern, replacement);
};
replaceOnce(/const WIDTH = \d+;/, `const WIDTH = ${image.width};`);
replaceOnce(/const HEIGHT = \d+;/, `const HEIGHT = ${image.height};`);
replaceOnce(/const BITS = \[[^\]]*\];/, `const BITS = ${JSON.stringify(image.bits)};`);
replaceOnce(/const message = "ARMIN SAYS HI";/, `const message = ${JSON.stringify(image.label)};`);
const effects = ["typewriter", "scanline", "rain", "fade", "crt", "glitch", "dissolve"];
const results = [];
for (const [effectIndex, effect] of effects.entries()) {
  let state = 12345, first = true, tick, stopped = false, interval;
  const math = Object.create(Math);
  math.random = () => {
    if (first) { first = false; return (effectIndex + 0.5) / effects.length; }
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 4294967296;
  };
  const context = vm.createContext({ Math: math, theme: { fg: (_token, text) => text },
    setInterval: (fn, ms) => { tick = fn; interval = Math.trunc(ms); return 1; },
    clearInterval: () => { stopped = true; },
  });
  vm.runInContext(stripTypeScriptTypes(source) + "\nglobalThis.component = new ArminComponent({ requestRender() {} });", context);
  const component = context.component;
  const widths = [0, 1, 12, 31, 32, 80];
  const initial = widths.map(width => component.render(width));
  const hashes = [];
  // Rain has empty columns that never settle in Pi. Bound the probe, not the implementation.
  for (let frame = 0; frame < 600 && !stopped; frame++) {
    tick();
    const lines = component.render(80);
    hashes.push(createHash("sha256").update(lines.join("\n")).digest("hex"));
  }
  const final = widths.map(width => { component.invalidate(); return component.render(width); });
  const completed = stopped;
  component.dispose();
  results.push({ effect, interval, initial, hashes, final, completed, disposed: stopped });
}
console.log(JSON.stringify(results));
