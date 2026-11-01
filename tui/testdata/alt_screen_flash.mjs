// Drive the installed pi-tui AltScreenFlashContainer with a manual clock for setTimeout, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { AltScreenFlashContainer } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/components/alt-screen-flash.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
let now = 0, seq = 0, timers = [];
globalThis.setTimeout = (fn, ms) => { const timer = { fn, at: now + ms, seq: seq++, live: true, unref() {} }; timers.push(timer); return timer; };
globalThis.clearTimeout = (timer) => { if (timer) timer.live = false; };
const advance = (ms) => {
  now += ms;
  for (;;) {
    const due = timers.filter((t) => t.live && t.at <= now).sort((a, b) => a.at - b.at || a.seq - b.seq)[0];
    if (!due) break;
    due.live = false;
    due.fn();
  }
};
const results = JSON.parse(input).map((probe) => {
  now = 0; seq = 0; timers = [];
  let renders = 0;
  const container = new AltScreenFlashContainer(() => { renders++; });
  const out = [];
  for (const op of probe) {
    if (op.flash !== undefined) container.flash(op.flash, op.duration === null ? undefined : op.duration);
    else if (op.dispose) container.dispose();
    else if (op.expire) advance(2000);
    else if (op.invalidate) container.invalidate();
    out.push({ rows: container.render(op.width ?? 80), renders });
  }
  return out;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
