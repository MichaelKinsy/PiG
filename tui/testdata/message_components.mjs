// Drive the installed Pi's summary and skill message components, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { CompactionSummaryMessageComponent } = await load("dist/modes/interactive/components/compaction-summary-message.js");
const { BranchSummaryMessageComponent } = await load("dist/modes/interactive/components/branch-summary-message.js");
const { SkillInvocationMessageComponent } = await load("dist/modes/interactive/components/skill-invocation-message.js");
const { ThemeSelectorComponent } = await load("dist/modes/interactive/components/theme-selector.js");
const { ShowImagesSelectorComponent } = await load("dist/modes/interactive/components/show-images-selector.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  let component;
  if (test.kind === "compaction") component = new CompactionSummaryMessageComponent({ role: "compactionSummary", summary: test.text, tokensBefore: test.tokens, timestamp: 0 }, undefined, test.pad);
  else if (test.kind === "branch") component = new BranchSummaryMessageComponent({ role: "branchSummary", summary: test.text, fromId: "x", timestamp: 0 }, undefined, test.pad);
  else if (test.kind === "skill") component = new SkillInvocationMessageComponent({ name: test.name, location: "/x/SKILL.md", content: test.text, userMessage: undefined }, undefined, test.pad);
  else if (test.kind === "theme") component = new ThemeSelectorComponent(test.name, () => {}, () => {}, () => {});
  else component = new ShowImagesSelectorComponent(test.current, () => {}, () => {});
  const frames = [];
  for (const expanded of test.kind === "images" || test.kind === "theme" ? [false] : [false, true]) {
    if (test.kind !== "images" && test.kind !== "theme") component.setExpanded(expanded);
    frames.push(component.render(test.width));
  }
  return { frames };
});
process.stdout.write(JSON.stringify(results));
