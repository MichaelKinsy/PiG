// Drive the installed Pi's AssistantMessageComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { AssistantMessageComponent } = await load("dist/modes/interactive/components/assistant-message.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  const content = test.content.map((block) => (block.thinking ? { type: "thinking", thinking: block.text } : { type: "text", text: block.text }));
  const message = { role: "assistant", content, api: "openai-completions", provider: "p", model: "m", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: 0 };
  const component = new AssistantMessageComponent(message, test.hide, undefined, test.label ?? undefined, 1);
  const frames = [component.render(test.width)];
  for (const op of test.ops) {
    if (op.kind === "label") component.setHiddenThinkingLabel(op.value);
    else if (op.kind === "hide") component.setHideThinkingBlock(op.value);
    frames.push(component.render(test.width));
  }
  return { frames };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
