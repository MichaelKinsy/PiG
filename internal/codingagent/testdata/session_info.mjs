// Run the installed Pi's handleSessionCommand (interactive-mode.ts, /session) over a stub session and formatCacheWarmingStatus, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
// The theme picks truecolor or 256 colors from the host TERM and COLORTERM; fix it so the probe output does not depend on them.
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark");
const { InteractiveMode } = await load("dist/modes/interactive/interactive-mode.js");
const { AgentSession } = await load("dist/core/agent-session.js");
const { formatCacheWarmingStatus } = await load("dist/core/cache-warmer.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { sessions, statuses } = JSON.parse(input);
const infos = sessions.map(({ id, name, entries, prices, mode, status, model }) => {
  const added = [];
  const runtime = { getModel: (provider, modelId) => (prices[`${provider}/${modelId}`] === undefined ? undefined : { cost: { cacheRead: prices[`${provider}/${modelId}`] } }) };
  const sessionManager = { getEntries: () => entries, getSessionName: () => name };
  const stub = {
    session: { modelRuntime: runtime, cacheWarmingStatus: status ?? undefined, model: model ?? undefined },
    sessionManager,
    settingsManager: { getCacheWarmingMode: () => mode },
    chatContainer: { addChild: (child) => added.push(child) },
    ui: { requestRender() {} },
  };
  stub.session.getSessionStats = () => AgentSession.prototype.getSessionStats.call({ sessionManager, sessionFile: undefined, sessionId: id, getContextUsage: () => undefined });
  InteractiveMode.prototype.handleSessionCommand.call(stub);
  return added[1].build();
});
const formatted = statuses.map(({ status, now }) => formatCacheWarmingStatus(status, now));
process.stdout.write(JSON.stringify({ infos, formatted }), () => process.exit(0));
