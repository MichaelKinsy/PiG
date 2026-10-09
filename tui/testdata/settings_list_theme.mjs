// Drive the installed Pi getSettingsListTheme for each theme, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const { setCapabilities } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
const { initTheme, getSettingsListTheme } = await import(pathToFileURL(root + "dist/modes/interactive/theme/theme.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  const t = getSettingsListTheme();
  return { cursor: t.cursor, rows: test.texts.map((text) => ({ label: [t.label(text, false), t.label(text, true)], value: [t.value(text, false), t.value(text, true)], description: t.description(text), hint: t.hint(text) })) };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
