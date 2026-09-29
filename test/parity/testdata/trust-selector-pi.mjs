import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve(process.env.PI_PACKAGE_ROOT ?? "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
// Chalk's non-color styles inspect FORCE_COLOR independently of Pi's RGB capability flag; this probe represents a styled terminal.
process.env.FORCE_COLOR = "3";
const { setCapabilities, setKeybindings } = await import(pathToFileURL(`${root}/node_modules/@earendil-works/pi-tui/dist/index.js`).href);
const { initTheme } = await import(pathToFileURL(`${root}/dist/modes/interactive/theme/theme.js`).href);
const { KeybindingsManager } = await import(pathToFileURL(`${root}/dist/core/keybindings.js`).href);
const { TrustSelectorComponent } = await import(pathToFileURL(`${root}/dist/modes/interactive/components/trust-selector.js`).href);
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark", false);
setKeybindings(new KeybindingsManager());
const out = [];
for (const [name, cwd, savedPath, trusted, key] of [["saved", "/project", "/project", true, "\x1b[B"], ["new", "/project", "", false, "\n"], ["ancestor", "/parent/project/nested", "/parent", true, ""], ["parent", "/parent/project", "/parent", true, "\n"]]) {
  let selection = null;
  const component = new TrustSelectorComponent({ cwd, savedDecision: savedPath ? { path: savedPath, decision: true } : null, projectTrusted: trusted, onSelect: value => { selection = { Trusted: value.trusted, Updates: value.updates.map(update => ({ Path: update.path, Decision: update.decision })) }; }, onCancel() {} });
  out.push({ name: `${name}-before`, lines: component.render(120), selection: null });
  component.handleInput(key);
  out.push({ name: `${name}-after`, lines: component.render(120), selection });
}
console.log(JSON.stringify(out));
