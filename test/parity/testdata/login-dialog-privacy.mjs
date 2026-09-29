import { pathToFileURL } from "node:url";
import { join } from "node:path";
const base = pathToFileURL(join(process.argv[2], "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/")).href;
const { SettingsManager } = await import(base + "core/settings-manager.js");
if (process.argv[3] === "settings") {
  const manager = SettingsManager.create(process.argv[4], process.argv[4]);
  const before = manager.getGlobalSettings().maskSecretInput;
  manager.setTheme("light");
  await manager.flush();
  console.log(JSON.stringify({ value: before, errors: manager.drainErrors(), theme: manager.getTheme() }));
} else {
  const { initTheme } = await import(base + "modes/interactive/theme/theme.js");
  const { LoginDialogComponent } = await import(base + "modes/interactive/components/login-dialog.js");
  const { InteractiveMode } = await import(base + "modes/interactive/interactive-mode.js");
  initTheme("dark");
  const frames = [];
  for (const value of ["", "abcd", "abcde-12345", "x😀界éZ"]) {
    const dialog = new LoginDialogComponent({ requestRender() {} }, "Test", () => {});
    dialog.focused = true;
    const answer = InteractiveMode.prototype.showAuthPrompt.call({}, dialog, { type: "secret", message: "API key", placeholder: "sample" });
    frames.push(dialog.render(100));
    dialog.handleInput(value);
    frames.push(dialog.render(100));
    dialog.handleInput("\r");
    if (await answer !== value) throw new Error("submission changed");
    frames.push(dialog.render(100));
    dialog.showProgress("Checking credentials...");
    frames.push(dialog.render(100));
  }
  console.log(JSON.stringify(frames));
}
