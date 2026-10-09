// Drive the installed Pi's FooterComponent over a stub session, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
delete process.env.PI_EXPERIMENTAL;
const load = (path) => import(pathToFileURL(root + path));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { FooterComponent } = await load("dist/modes/interactive/components/footer.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const usage = (u) => ({ input: u.input, output: u.output, cacheRead: u.cacheRead, cacheWrite: u.cacheWrite, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: u.cost ?? 0 } });
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  process.env.HOME = test.home;
  if (test.experimental) process.env.PI_EXPERIMENTAL = "1"; else delete process.env.PI_EXPERIMENTAL;
  const model = { id: test.model, provider: test.provider, reasoning: test.reasoning, contextWindow: test.contextWindow };
  const entries = [{ type: "usage", usage: usage(test.totals) }];
  if (test.latest) entries.push({ type: "message", message: { role: "assistant", usage: usage({ ...test.latest, output: 0 }) } });
  const session = {
    state: { model, thinkingLevel: test.thinkingLevel },
    model,
    routedModel: test.routed ? { model: { id: test.routed.id, contextWindow: test.routed.contextWindow }, thinkingLevel: test.routed.level || undefined } : undefined,
    sessionManager: { getEntryCount: () => entries.length, getSessionId: () => "s", getLeafId: () => "l", getEntries: () => entries, getCwd: () => test.cwd, getSessionName: () => test.name || undefined },
    // AgentSession.getContextUsage reads the window of the limits model: the routed model when one answered last (agent-session.ts:4139-4144).
    getContextUsage: () => ({ tokens: null, contextWindow: test.routed ? test.routed.contextWindow : test.contextWindow, percent: test.percent }),
    modelRuntime: { isUsingSubscription: () => test.subscription },
  };
  const footerData = {
    getGitBranch: () => test.branch || null,
    getAvailableProviderCount: () => test.providers,
    getExtensionStatuses: () => new Map(Object.entries(test.statuses ?? {})),
  };
  const footer = new FooterComponent(session, footerData);
  footer.setAutoCompactEnabled(test.auto);
  return footer.render(test.width);
});
process.stdout.write(JSON.stringify(results));
