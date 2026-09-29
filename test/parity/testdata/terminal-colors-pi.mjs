import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { mock } from "node:test";
const root = fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent", import.meta.url));
if (JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version !== "0.87.1") throw new Error("Expected Pi 0.87.1");
const { TuiMainScreen, parseOsc11BackgroundColor, parseTerminalColorSchemeReport } = await import(pathToFileURL(join(root,"node_modules/@earendil-works/pi-tui/dist/index.js")));
const lines = [];
const color = value => value ? `${value.r},${value.g},${value.b}` : "undefined";
for (const value of ["rgba:0000/8000/ffff/0000", "rgb:"+"f".repeat(64)+"/0/0", "rgb:"+"f".repeat(256)+"/0/0", "\ufeff#ffffff\ufeff", "\u0085#ffffff\u0085", "#+1+2+3", "#-1-2-3"]) lines.push("parsed:"+color(parseOsc11BackgroundColor("\x1b]11;"+value+"\x07")));
lines.push("scheme:"+parseTerminalColorSchemeReport("\x1b[?997;2n\x1b[?997;1n\x1b[?997;1n"));
class Terminal {
  columns=80; rows=24; kittyProtocolActive=false;
  start(onInput) { this.input = onInput; }
  stop() { this.input=undefined; }
  write(data) { if (data === "\x1b]11;?\x07") lines.push("write:"+data); }
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
try {
  const first=ui.queryTerminalBackgroundColor({timeoutMs:1});
  const second=ui.queryTerminalBackgroundColor({timeoutMs:1000});
  let secondSettled=false;
  second.then(()=>{secondSettled=true;});
  mock.timers.tick(5);
  lines.push("first:"+color(await first));
  send("x");
  send("\x1b]11;#000000\x07");
  await Promise.resolve();
  lines.push("second-pending:"+!secondSettled);
  send("\x1b]11;#ffffff\x07");
  lines.push("second:"+color(await second));
  const malformed=ui.queryTerminalBackgroundColor({timeoutMs:1000});
  send("\x1b]11;not-a-color\x07");
  lines.push("malformed:"+color(await malformed));
  send("\x1b]11;#000000\x07");
} finally { ui.stop(); mock.timers.reset(); }
console.log(JSON.stringify(lines));
