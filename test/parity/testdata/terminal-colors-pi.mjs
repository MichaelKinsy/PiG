import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { mock } from "node:test";
const root = fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent", import.meta.url));
if (JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version !== "1.1.0") throw new Error("Expected Pi 1.1.0");
const { TuiMainScreen, parseTerminalColorSchemeReport } = await import(pathToFileURL(join(root,"node_modules/@earendil-works/pi-tui/dist/index.js")));
const { parseOscColorResponse } = await import(pathToFileURL(join(root,"node_modules/@earendil-works/pi-tui/dist/terminal-colors.js")));
const lines = [];
const color = value => value ? `${value.r},${value.g},${value.b}` : "undefined";
const parsed = data => { const reply = parseOscColorResponse(data); return reply ? `${reply.target}:${color(reply.rgb)}` : "undefined"; };
for (const value of ["rgba:0000/8000/ffff/0000", "rgb:"+"f".repeat(64)+"/0/0", "rgb:"+"f".repeat(256)+"/0/0", "\ufeff#ffffff\ufeff", "\u0085#ffffff\u0085", "#+1+2+3", "#-1-2-3"]) lines.push("parsed:"+parsed("\x1b]11;"+value+"\x07"));
for (const data of ["\x1b]10;#808080\x1b\\", "\x1b]4;7;rgb:ffff/0000/8000\x07", "\x1b]4;999;#010203\x07", "\x1b]12;#010203\x07", "\x1b]11;#ffffff", "\x1b]11;not-a-color\x07"]) lines.push("parsed:"+parsed(data));
lines.push("scheme:"+parseTerminalColorSchemeReport("\x1b[?997;2n\x1b[?997;1n\x1b[?997;1n"));
class Terminal {
  columns=80; rows=24; kittyProtocolActive=false;
  start(onInput) { this.input = onInput; }
  stop() { this.input=undefined; }
  write(data) { if (data.startsWith("\x1b]10;")) lines.push("write:"+data); }
  hideCursor() {} showCursor() {} moveBy() {} clearLine() {} clearFromCursor() {} clearScreen() {} setTitle() {} setProgress() {}
}
mock.timers.enable({apis:["setTimeout"]});
const terminal = new Terminal();
const ui = new TuiMainScreen(terminal);
let listenerCalls=0, focusCalls=0;
const component = {render:()=>[], invalidate:()=>{}, handleInput:()=>focusCalls++};
ui.addChild(component); ui.setFocus(component);
ui.addInputListener(()=>{listenerCalls++;});
ui.start();
const send = data => {
  const before=listenerCalls;
  terminal.input(data);
  if (listenerCalls !== focusCalls) throw new Error("listener/focus disagreement");
  lines.push("consumed:"+(before===listenerCalls));
};
// Deterministic palette (D-D): the same fixed OSC 10/11/4 replies drive both implementations.
const hex = (r,g,b) => "#"+[r,g,b].map(n=>n.toString(16).padStart(2,"0")).join("");
const palette = index => hex(index*16, 255-index*16, index);
const da1 = "\x1b[?62;4;52c";
const desc = c => `fg=${color(c.foreground)};bg=${color(c.background)};palette=${c.palette ? c.palette.map(color).join("|") : "undefined"}`;
try {
  const first=ui.queryTerminalColors({timeoutMs:1});
  const second=ui.queryTerminalColors({timeoutMs:1000});
  let secondSettled=false;
  second.then(()=>{secondSettled=true;});
  mock.timers.tick(5);
  lines.push("first:"+desc(await first));
  send("x");
  send("\x1b]11;#000000\x07");
  await Promise.resolve();
  lines.push("second-pending:"+!secondSettled);
  send(da1);
  send("\x1b]10;"+"#808080\x07");
  send("\x1b]11;#ffffff\x07");
  send("\x1b]11;#123456\x07");
  for (let index = 0; index < 16; index++) send("\x1b]4;"+index+";"+palette(index)+"\x07");
  lines.push("second:"+desc(await second));
  send(da1);
  const malformed=ui.queryTerminalColors({timeoutMs:1000});
  send("\x1b]11;not-a-color\x07");
  send(da1);
  lines.push("malformed:"+desc(await malformed));
  send(da1);
  send("\x1b]11;#000000\x07");
} finally { ui.stop(); mock.timers.reset(); }
console.log(JSON.stringify(lines));
