// Drive the installed Pi's LoginDialogComponent (the login flow's dialog: details, info with links, waiting and progress lines, device code, manual and ordinary prompts, cancel and submit) with seeded steps and render it at several widths, never a translated oracle.
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
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const flush = () => new Promise((resolve) => setImmediate(resolve));
const results = [];
for (const probe of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: true });
  initTheme(probe.theme);
  setKeybindings(new KeybindingsManager({}));
  const events = [];
  const tui = { requestRender() {}, terminal: { rows: 24, columns: 80 } };
  const dialog = new LoginDialogComponent(tui, probe.providerId, (success, message) => events.push(`complete:${success}:${message ?? ""}`), probe.providerName || undefined, probe.title || undefined);
  dialog.focused = true;
  const frames = [];
  for (const step of probe.steps) {
    events.length = 0;
    const track = (promise, label) => promise.then((value) => events.push(`${label}:resolved:${value}`), (error) => events.push(`${label}:rejected:${error.message}`));
    switch (step.op) {
      case "details": dialog.showDetails(step.lines); break;
      case "info": dialog.showInfo(step.message, step.links, step.closeHint); break;
      case "waiting": dialog.showWaiting(step.message); break;
      case "progress": dialog.showProgress(step.message); break;
      case "deviceCode": dialog.showDeviceCode({ verificationUri: step.uri, userCode: step.code, ...(step.interval ? { interval: step.interval } : {}) }); break;
      case "manual": track(dialog.showManualInput(step.message), "manual"); break;
      case "prompt": track(dialog.showPrompt(step.message, step.placeholder || undefined), "prompt"); break;
      case "keys": dialog.handleInput(step.keys); break;
    }
    await flush();
    await flush();
    frames.push({ events: [...events], rows: dialog.render(step.width) });
  }
  results.push(frames);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
