// Drive the installed Pi DynamicBorder, ThemedText, IdleStatus and StatusIndicator (renderInBorder, renderSpinnerInBorder), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
globalThis.setInterval = () => 0;
globalThis.clearInterval = () => {};
const load = (path) => import(pathToFileURL(root + path));
const { DynamicBorder } = await load("dist/modes/interactive/components/dynamic-border.js");
const { ThemedText } = await load("dist/modes/interactive/components/themed-text.js");
const { IdleStatus, StatusIndicator } = await load("dist/modes/interactive/components/status-indicator.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const tag = (name) => (text) => `<${name}>${text}</${name}>`;
const results = JSON.parse(input).map((test) => {
  switch (test.kind) {
    case "border": { const border = new DynamicBorder(tag("b")); return test.widths.map((w) => border.render(w)); }
    case "idle": { const idle = new IdleStatus(); return test.widths.map((w) => idle.render(w)); }
    case "themed": {
      let state = test.texts[0];
      const text = new ThemedText(() => state, test.paddingX, test.paddingY);
      return test.ops.map((op) => {
        if (op.text !== undefined && op.text !== null) state = op.text;
        if (op.invalidate) text.invalidate();
        return text.render(op.width);
      });
    }
    case "status": {
      const indicator = new StatusIndicator("working", { requestRender() {} }, tag("sp"), tag("msg"), test.message, test.indicator ?? undefined);
      const out = [];
      for (const step of test.steps) {
        if (step.message !== undefined && step.message !== null) indicator.setMessage(step.message);
        for (let i = 0; i < step.ticks; i++) {
          indicator.currentFrame = (indicator.currentFrame + 1) % Math.max(1, indicator.frames.length);
          indicator.updateDisplay();
        }
        out.push(test.widths.map((w) => ({ border: indicator.renderInBorder(w), spinner: indicator.renderSpinnerInBorder(w) })));
      }
      return out;
    }
  }
});
process.stdout.write(JSON.stringify(results));
