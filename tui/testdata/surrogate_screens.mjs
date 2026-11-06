// Pi's TuiAltScreen and TuiMainScreen over rows holding lone UTF-16 surrogates; run by surrogate_screens_oracle_test.go.
// Every terminal write is encoded with Buffer.from, as process.stdout.write encodes it, and reported as hex per step.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { runRequestedFrame } from "./frame-clock.mjs";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { TuiAltScreen, TuiMainScreen, setCapabilities } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
for (const name of ["TMUX", "ZELLIJ", "STY", "WEZTERM_PANE", "TERM_PROGRAM"]) delete process.env[name];
process.env.TERM = "xterm-256color";
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const line = (segs) => segs.map((s) => (s.t ?? "").repeat(s.n ?? 1) + String.fromCharCode(...(s.u ?? []))).join("");
const out = [];
for (const c of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: true });
  let writes = [];
  const term = {
    columns: c.cols, rows: c.rows, kittyProtocolActive: false,
    start() { writes.push("<terminal.start>"); }, stop() { writes.push("<terminal.stop>"); }, async drainInput() {},
    write(d) { writes.push(d); },
    moveBy(n) { if (n > 0) writes.push(`\x1b[${n}B`); else if (n < 0) writes.push(`\x1b[${-n}A`); },
    hideCursor() { writes.push("\x1b[?25l"); }, showCursor() { writes.push("\x1b[?25h"); },
    clearLine() { writes.push("\x1b[K"); }, clearFromCursor() { writes.push("\x1b[J"); }, clearScreen() { writes.push("\x1b[2J\x1b[H"); },
    setTitle() {}, setProgress() {},
  };
  const take = () => { if (c.mode !== "alt") writes = writes.filter((w) => w !== "<terminal.stop>"); const h = writes.map((w) => Buffer.from(w).toString("hex")).join(""); const n = writes.length; writes = []; return { hex: h, writes: n }; };
  let cur = c.mode === "alt" ? [] : c.lines1.map(line);
  const comp = { invalidate() {}, render() { return cur.slice(); } };
  const r = { steps: [] };
  const step = (fn) => { let error = null; try { fn(); } catch (e) { error = String(e.message).split("\n")[0]; } r.steps.push({ ...take(), error }); };
  if (c.mode === "alt") {
    const ui = new TuiAltScreen(term);
    ui.addChild(comp);
    ui.start();
    await runRequestedFrame(ui);
    take();
    cur = c.lines1.map(line);
    step(() => ui.renderNow(false));
    cur = c.lines2.map(line);
    step(() => ui.renderNow(false));
    ui.stop({ preserveScreen: false });
    const i = writes.indexOf("<terminal.stop>");
    if (i > 0 && writes[i - 1] === "\x1b[?25h") writes.splice(i - 1, 2);
    r.steps.push({ ...take(), error: null });
  } else {
    const ui = new TuiMainScreen(term);
    ui.addChild(comp);
    step(() => ui.renderNow());
    cur = c.lines2.map(line);
    step(() => ui.renderNow());
  }
  out.push(r);
}
process.stdout.write(JSON.stringify(out), () => process.exit(0));
