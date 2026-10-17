// Drive the installed Pi CustomEditor with an embedded working status indicator through a bracketed paste, some Up keys and ticks, then render: its top border carries the status and the overflow label, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, theme } = await load("dist/modes/interactive/theme/theme.js");
const { CustomEditor } = await load("dist/modes/interactive/components/custom-editor.js");
const { WorkingStatusIndicator } = await load("dist/modes/interactive/components/status-indicator.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(probe.theme);
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const borderColor = theme.getThinkingBorderColor(probe.level);
  const editor = new CustomEditor(tui, { borderColor, selectList: {} }, new KeybindingsManager({}), { paddingX: probe.paddingX, embedWorkingStatus: true });
  editor.borderColor = borderColor;
  editor.focused = true;
  // Typed rows, not a paste: a long paste collapses into one marker and never scrolls.
  for (let i = 0; i < probe.rows; i++) { if (i > 0) editor.handleInput("\n"); for (const ch of `row ${i}`) editor.handleInput(ch); }
  for (let i = 0; i < probe.ups; i++) editor.handleInput("\x1b[A");
  const indicator = new WorkingStatusIndicator(tui, probe.message, probe.frames ? { frames: probe.frames } : undefined, (text) => editor.borderColor(text));
  for (let i = 0; i < probe.ticks; i++) {
    indicator.currentFrame = (indicator.currentFrame + 1) % Math.max(1, indicator.frames.length);
    indicator.updateDisplay();
  }
  editor.setWorkingStatusIndicator(indicator);
  const rows = editor.render(probe.width);
  indicator.dispose();
  return rows;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
