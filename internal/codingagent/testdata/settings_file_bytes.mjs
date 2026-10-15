// Drive the installed Pi's SettingsManager over scripted setter calls on an existing settings.json and report the file bytes afterwards.
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SettingsManager } = await import(pathToFileURL(root + "dist/core/settings-manager.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const script of JSON.parse(input)) {
  const agentDir = mkdtempSync(join(tmpdir(), "pi-settings-"));
  const cwd = join(agentDir, "project");
  mkdirSync(cwd, { recursive: true });
  if (script.initial !== null) writeFileSync(join(agentDir, "settings.json"), script.initial);
  const manager = SettingsManager.create(cwd, agentDir);
  for (const op of script.ops) {
    switch (op.op) {
      case "theme": manager.setTheme(op.value); break;
      case "model": manager.setDefaultModelAndProvider(op.provider, op.model); break;
      case "prefix": manager.setShellCommandPrefix(op.value); break;
      case "npm": manager.setNpmCommand(op.value); break;
      case "enabledModels": manager.setEnabledModels(op.value); break;
      case "extensions": manager.setExtensionPaths(op.value); break;
      case "changelog": manager.setLastChangelogVersion(op.value); break;
      case "compaction": manager.setCompactionEnabled(op.value); break;
      case "imageWidth": manager.setImageWidthCells(op.value); break;
      case "padding": manager.setEditorPaddingX(op.value); break;
      case "shellPath": manager.setShellPath(op.value); break;
      case "transport": manager.setTransport(op.value); break;
    }
  }
  await manager.flush();
  let text = null;
  try { text = readFileSync(join(agentDir, "settings.json"), "utf8"); } catch {}
  results.push(text);
}
process.stdout.write(JSON.stringify(results));
