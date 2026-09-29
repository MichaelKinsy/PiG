// Runs the installed, pinned Pi implementation, not a copy of its detector.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
const version = JSON.parse(readFileSync(root + "package.json", "utf8")).version;
if (version !== process.argv[2]) throw new Error(`Pi oracle ${version}, expected ${process.argv[2]}`);
const { detectCapabilities, resetCapabilitiesCache } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/terminal-image.js"));
const { parseOsc11BackgroundColor } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/terminal-colors.js"));
const { getThemeByName, detectTerminalBackgroundFromEnv } = await import(pathToFileURL(root + "dist/modes/interactive/theme/theme.js"));
const cases = JSON.parse(readFileSync(0, "utf8"));
const results = cases.map(({ env, platform, osc }) => {
  process.env = { ...env, PI_PACKAGE_DIR: root };
  Object.defineProperty(process, "platform", { value: platform });
  resetCapabilitiesCache();
  const capabilities = detectCapabilities(() => false);
  const themes = {};
  for (const name of ["dark", "light"]) {
    const theme = getThemeByName(name);
    const json = JSON.parse(readFileSync(root + "dist/modes/interactive/theme/" + name + ".json", "utf8"));
    const colors = {};
    for (const token of Object.keys(json.colors)) {
      colors[token] = token.endsWith("Bg") ? theme.getBgAnsi(token) : theme.getFgAnsi(token);
    }
    themes[name] = { mode: theme.getColorMode(), colors };
  }
  return { capabilities, themes, background: detectTerminalBackgroundFromEnv(), rgb: parseOsc11BackgroundColor(osc ?? "") ?? null };
});
process.stdout.write(JSON.stringify(results));
