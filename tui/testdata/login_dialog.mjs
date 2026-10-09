// Drive the installed Pi's LoginDialogComponent input handling, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { LoginDialogComponent } = await load("dist/modes/interactive/components/login-dialog.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const tick = () => new Promise((resolve) => setImmediate(resolve));
const results = [];
for (const test of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  const dialog = new LoginDialogComponent({ requestRender() {} }, "anthropic", (ok, message) => events.push(`complete:${ok}:${message ?? ""}`));
  dialog.focused = true;
  const pending = test.manual ? dialog.showManualInput("Paste code") : dialog.showPrompt("Enter key", "sk-...");
  pending.then((value) => events.push("submit:" + value), (error) => events.push("reject:" + error.message));
  await tick();
  const steps = [], frames = [dialog.render(test.width)];
  for (const key of test.keys) {
    dialog.handleInput(key);
    await tick();
    steps.push({ events: events.slice(), aborted: dialog.signal.aborted });
    frames.push(dialog.render(test.width));
  }
  results.push({ steps, frames });
}
process.stdout.write(JSON.stringify(results));
