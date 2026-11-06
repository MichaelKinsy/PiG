// Draw the installed Pi's codemode ToolExecutionComponent through Pi's own TuiMainScreen (a header row first, so the card reaches the
// differential render loop), and report the bytes written to the terminal or the error doRender threw. Never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { setKeybindings, setCapabilities, TuiMainScreen } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ToolExecutionComponent } = await load("dist/modes/interactive/components/tool-execution.js");
const { codemodeRenderers } = await load("dist/extensions/codemode/renderer.js");
let input = "";
const clockStart = 1_700_000_000_000;
Date.now = () => clockStart;
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager({}));
  const def = { name: "codemode", label: "codemode", description: "", parameters: {}, ...codemodeRenderers };
  const card = new ToolExecutionComponent(test.tool, "id1", test.args, { showImages: true }, def, { requestRender() {} }, test.cwd);
  card.markExecutionStarted();
  card.setArgsComplete();
  card.updateResult(test.result, false);
  card.setExpanded(test.expanded);
  const writes = [];
  const terminal = { columns: test.width, rows: 40, start() {}, stop() {}, write: (data) => writes.push(data), hideCursor: () => writes.push("\x1b[?25l"), showCursor: () => writes.push("\x1b[?25h") };
  const screen = new TuiMainScreen(terminal);
  let shown = false;
  screen.addChild({ render: (width) => ["header", ...(shown ? card.render(width) : [])], invalidate() { card.invalidate(); } });
  let error = null;
  try {
    screen.renderNow();
    writes.length = 0;
    shown = true;
    screen.renderNow();
  } catch (e) {
    error = String(e.message).split("\n")[0];
  }
  return { error, output: writes.join("") };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
