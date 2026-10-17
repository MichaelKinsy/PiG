// Drive the installed Pi AssistantMessageComponent with whole messages (text, thinking, tool calls, stop reasons), byte for byte, never a translated oracle.
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
const message = (spec) => ({
  role: "assistant",
  content: (spec.blocks ?? []).map((b) => (b.kind === "tool" ? { type: "toolCall", id: "t", name: "bash", arguments: {} } : b.kind === "thinking" ? { type: "thinking", thinking: b.text } : { type: "text", text: b.text })),
  api: "openai-completions", provider: "p", model: "m",
  usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
  stopReason: spec.stopReason, ...(spec.errorMessage ? { errorMessage: spec.errorMessage } : {}), timestamp: 0,
});
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  const component = new AssistantMessageComponent(message(test.message), test.hide, undefined, test.label ?? undefined, test.pad);
  const frames = [test.widths.map((w) => component.render(w))];
  for (const op of test.ops ?? []) {
    if (op.kind === "pad") component.setOutputPad(op.n);
    else if (op.kind === "label") component.setHiddenThinkingLabel(op.text);
    else if (op.kind === "hide") component.setHideThinkingBlock(op.flag);
    else if (op.kind === "update") component.updateContent(message(op.message), op.streaming);
    frames.push(test.widths.map((w) => component.render(w)));
  }
  return frames;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
