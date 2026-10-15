// Run the installed Pi's getSessionStats (agent-session.ts), getUsageCostBreakdown (usage-totals.ts), computeCacheWaste (cache-stats.ts) and
// the footer's formatTokens over session entries, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const load = (path) => import(pathToFileURL(root + path));
const { AgentSession } = await load("dist/core/agent-session.js");
const { getUsageCostBreakdown } = await load("dist/core/usage-totals.js");
const { computeCacheWaste } = await load("dist/core/cache-stats.js");
const { formatTokens } = await load("dist/modes/interactive/components/footer.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { sessions, numbers } = JSON.parse(input);
const out = { sessions: sessions.map(({ entries, prices }) => {
  const models = { getModel: (provider, id) => (prices[`${provider}/${id}`] === undefined ? undefined : { cost: { cacheRead: prices[`${provider}/${id}`] } }) };
  const stub = { sessionManager: { getEntries: () => entries }, sessionFile: undefined, sessionId: "id", getContextUsage: () => undefined };
  const stats = AgentSession.prototype.getSessionStats.call(stub);
  delete stats.contextUsage;
  return { stats, breakdown: getUsageCostBreakdown(entries), waste: computeCacheWaste(entries, models) };
}), numbers: numbers.map((n) => ({ locale: n.toLocaleString(), tokens: formatTokens(n) })) };
process.stdout.write(JSON.stringify(out), () => process.exit(0));
