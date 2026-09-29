// Execute the pinned source, not a second implementation. No real timers or user config.
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";
import { createHash } from "node:crypto";

const source = readFileSync(new URL("../../../.upstream/current/packages/coding-agent/src/modes/interactive/components/armin.ts", import.meta.url), "utf8")
  .replace(/^import .*;\n/gm, "")
  .replace("export class ArminComponent", "class ArminComponent");
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
