// Open each scripted session file in the installed Pi's AgentSession and run exportToHtml (agent-session.ts) with the given theme, then report the page with its embedded session data decoded and normalized, or the error.
import { copyFileSync, readFileSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createAgentSession, createAgentSessionServices, createAgentSessionFromServices, ModelRuntime, SessionManager, SettingsManager } = await import(pathToFileURL(root + "dist/index.js"));
const { AuthStorage } = await import(pathToFileURL(root + "dist/core/auth-storage.js"));
const { InMemoryModelsStore } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/models-store.js"));
const templateless = (page) => {
  const open = "\n  <script>\n", close = "\n  </script>";
  const at = page.lastIndexOf(open);
  const end = at < 0 ? -1 : page.indexOf(close, at);
  return end < 0 ? page : page.slice(0, at + open.length) + "<TEMPLATE_JS>" + page.slice(end);
};
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const scenario of JSON.parse(input)) {
  const cwd = scenario.dir;
  const modelRuntime = await ModelRuntime.create({ credentials: AuthStorage.inMemory({ anthropic: { type: "api_key", key: "k1" } }), modelsStore: new InMemoryModelsStore() });
  const settingsManager = SettingsManager.create(cwd, cwd);
  if (scenario.settingsTheme) settingsManager.setTheme(scenario.settingsTheme);
  const sessionFile = join(cwd, "pi-session.jsonl");
  copyFileSync(join(cwd, "source.jsonl"), sessionFile);
  let out;
  try {
    const sessionManager = SessionManager.open(sessionFile, undefined, cwd);
    const model = modelRuntime.getModel("anthropic", "claude-sonnet-4-5");
    const { session } = scenario.viaServices
      ? await createAgentSessionFromServices({ services: await createAgentSessionServices({ cwd, agentDir: cwd, modelRuntime, settingsManager }), sessionManager, model, noTools: "all" })
      : await createAgentSession({ cwd, agentDir: cwd, modelRuntime, model, noTools: "all", settingsManager, sessionManager });
    const path = await session.exportToHtml(join(cwd, "pi-export.html"), scenario.theme ? { themeName: scenario.theme } : {});
    const html = readFileSync(path, "utf8");
    const match = /<script id="session-data" type="application\/json">([^<]*)<\/script>/.exec(html);
    const data = JSON.parse(Buffer.from(match[1], "base64").toString("utf8"));
    const ids = new Map();
    data.entries.forEach((e, i) => ids.set(e.id, i));
    const { systemPrompt, tools, ...rest } = data;
    const { id: hid, timestamp: hts, cwd: hcwd, ...header } = rest.header ?? {};
    out = {
      html: templateless(html.replace(match[1], "<DATA>").replaceAll(cwd, "<cwd>")),
      data: { ...rest, header: rest.header ? header : null, leafId: rest.leafId === null ? null : ids.get(rest.leafId) ?? "?",
        entries: rest.entries.map((e) => { const { id, parentId, timestamp, ...r } = e; if (r.message) { const { timestamp: t, ...m } = r.message; r.message = m; } return { ...r, parent: parentId === null || parentId === undefined ? null : ids.get(parentId) ?? "?" }; }) },
    };
    session.dispose?.();
  } catch (error) {
    out = { thrown: String(error && error.message).replaceAll(cwd, "<cwd>") };
  }
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
