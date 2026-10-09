// Drive the installed Pi's ToolExecutionComponent with an MCP tool's renderers (mcp/tools.ts createMcpToolRenderers), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ToolExecutionComponent } = await load("dist/modes/interactive/components/tool-execution.js");
const { createMcpToolRenderers } = await load("dist/extensions/mcp/tools.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager({}));
  const def = { name: "mcp__srv__tool", label: "srv/tool", description: "", parameters: {}, ...createMcpToolRenderers("srv/tool") };
  const component = new ToolExecutionComponent(test.tool, "id1", test.args, { showImages: !test.noImages }, def, { requestRender() {} }, test.cwd);
  component.markExecutionStarted();
  component.setArgsComplete();
  if (test.result) component.updateResult(test.result, test.partial === true);
  component.setExpanded(test.expanded);
  return component.render(test.width);
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
