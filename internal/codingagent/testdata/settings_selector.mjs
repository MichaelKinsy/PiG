// Drive the installed Pi's SettingsSelectorComponent, never a translated oracle. Pi has no PiG "Mask secret input" row (D80), so the
// driver loads a copy of Pi's settings-selector.js with that one row and its callback inserted where Pig puts them.
import { mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const url = (path) => pathToFileURL(path).href;
const load = (path) => import(url(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");

const component = root + "dist/modes/interactive/components/settings-selector.js";
const bare = { "@earendil-works/pi-ai": root + "node_modules/@earendil-works/pi-ai/dist/index.js", "@earendil-works/pi-tui": root + "node_modules/@earendil-works/pi-tui/dist/index.js" };
let source = readFileSync(component, "utf8").replace(/from "([^"]+)"/g, (_, spec) => {
  if (bare[spec]) return `from "${url(bare[spec])}"`;
  if (spec.startsWith(".")) return `from "${url(resolve(dirname(component), spec))}"`;
  throw new Error("unexpected import " + spec);
});
const rowAnchor = `            {
                id: "warnings",`;
const caseAnchor = `                case "show-hardware-cursor":`;
if (!source.includes(rowAnchor) || !source.includes(caseAnchor)) throw new Error("settings-selector.js layout changed");
source = source.replace(rowAnchor, `            {
                id: "mask-secret-input",
                label: "Mask secret input",
                description: "PiG default: hide secret input with a count and last four characters. Differs from Pi; false restores Pi's plain-text behavior.",
                currentValue: config.maskSecretInput ? "true" : "false",
                values: ["true", "false"],
            },
` + rowAnchor).replace(caseAnchor, `                case "mask-secret-input":
                    callbacks.onMaskSecretInputChange(newValue === "true");
                    break;
` + caseAnchor);
const patched = join(mkdtempSync(join(tmpdir(), "pi-settings-oracle-")), "settings-selector.mjs");
writeFileSync(patched, source);
const { SettingsSelectorComponent } = await import(url(patched));

const callbackNames = ["onAutoCompactChange", "onShowImagesChange", "onImageWidthCellsChange", "onAutoResizeImagesChange", "onBlockImagesChange", "onEnableSkillCommandsChange", "onSteeringModeChange", "onFollowUpModeChange", "onTransportChange", "onHttpIdleTimeoutMsChange", "onCacheWarmingModeChange", "onModelThinkingLevelChange", "onModelThinkingLevelRemove", "onThemeChange", "onThemePreview", "onHideThinkingBlockChange", "onMermaidRenderingModeChange", "onShowCacheMissNoticesChange", "onCollapseChangelogChange", "onEnableInstallTelemetryChange", "onDoubleEscapeActionChange", "onTreeFilterModeChange", "onShowHardwareCursorChange", "onEditorPaddingXChange", "onOutputPadChange", "onAutocompleteMaxVisibleChange", "onQuietStartupChange", "onDefaultProjectTrustChange", "onClearOnShrinkChange", "onShowTerminalProgressChange", "onTuiModeChange", "onFullscreenExitOutputChange", "onFullscreenScrollbarChange", "onFullscreenCopyOnSelectChange", "onFullscreenWheelScrollLinesChange", "onWarningsChange", "onMaskSecretInputChange", "onCancel"];
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: test.images ? "kitty" : null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  const callbacks = {};
  for (const name of callbackNames) callbacks[name] = (...args) => events.push([name, ...args]);
  const selector = new SettingsSelectorComponent(structuredClone(test.config), callbacks);
  const steps = [events.length], frames = [selector.render(test.width)];
  for (const key of test.keys) {
    selector.getSettingsList().handleInput(key);
    steps.push(events.length);
    frames.push(selector.render(test.width));
  }
  return { events: JSON.parse(JSON.stringify(events)), steps, frames };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
