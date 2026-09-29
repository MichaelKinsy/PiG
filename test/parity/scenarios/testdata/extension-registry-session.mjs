// Records ctx.modelRegistry and ctx.sessionManager for parity scenarios.
//
// `/registry-info <file>` exercises every ModelRegistry member and the
// ReadonlySessionManager reads over a session forked from
// registry-session.jsonl (a branched tree with labels, a branch summary, a
// compaction and a context edit), and writes what
// they return to <file> with object keys sorted, so the artifact compares
// values, not the key order of Node's and Go's JSON encoders. Values that are
// random per run (entry ids, timestamps, session ids) are reduced to their
// relationships.
import { writeFileSync } from "node:fs";

const sortedKeys = (value) => {
  if (Array.isArray(value)) return value.map(sortedKeys);
  if (value instanceof Map) return sortedKeys(Object.fromEntries(value));
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortedKeys(value[key])]));
  }
  return value;
};

const REGISTRY_MEMBERS = [
  "refresh", "getError", "getAll", "getAvailable", "find", "hasConfiguredAuth", "getApiKeyAndHeaders",
  "getProviderAuthStatus", "getProvider", "stream", "streamSimple", "complete", "getProviderDisplayName",
  "getProviderAuth", "getApiKeyForProvider", "isUsingOAuth", "registerProvider", "unregisterProvider",
  "getRegisteredProviderConfig", "getRegisteredNativeProvider", "getRegisteredProviderIds",
];
const SESSION_MEMBERS = [
  "getCwd", "getSessionDir", "getSessionId", "getSessionFile", "getLeafId", "getLeafEntry", "getEntry",
  "getLabel", "getBranch", "buildContextEntries", "buildSessionProjection", "getHeader", "getEntries",
  "getTree", "getSessionName",
];

const model = (m) => m && {
  id: m.id, name: m.name, provider: m.provider, api: m.api, baseUrl: m.baseUrl, reasoning: m.reasoning,
  input: m.input, cost: m.cost, contextWindow: m.contextWindow, maxTokens: m.maxTokens,
};
const parity = (m) => m.provider.startsWith("parity-");
const tree = (nodes) => nodes.map((node) => ({ type: node.entry.type, label: node.label ?? null, children: tree(node.children) }));

export default function (pi) {
  pi.registerProvider("parity-reg", {
    name: "Parity Registry",
    baseUrl: "http://127.0.0.1:9/v1",
    apiKey: "parity-literal-key",
    api: "openai-completions",
    models: [{
      id: "reg-a", name: "Reg A", reasoning: false, input: ["text"],
      cost: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100,
    }],
  });
  pi.registerCommand("registry-info", {
    description: "Write ctx.modelRegistry and ctx.sessionManager results to a file",
    handler: async (args, ctx) => {
      await ctx.waitForIdle();
      const registry = ctx.modelRegistry;
      const session = ctx.sessionManager;
      const reg = registry.find("parity-reg", "reg-a");

      registry.registerProvider("parity-late", {
        baseUrl: "http://127.0.0.1:9/v1",
        apiKey: "parity-late-key",
        api: "openai-completions",
        models: [{
          id: "late-a", name: "Late A", reasoning: false, input: ["text"],
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 2000, maxTokens: 200,
        }],
      });
      const refreshed = await registry.refresh({ allowNetwork: false });

      const noteId = session.appendCustomEntry("registry-note", { n: 1 });
      const note = session.getEntry(noteId);
      const header = session.getHeader();
      const provider = registry.getProvider("parity-reg");
      const info = {
        registry: {
          members: REGISTRY_MEMBERS.filter((name) => typeof registry[name] === "function"),
          models: registry.getAll().filter(parity).map(model),
          available: registry.getAvailable().filter(parity).map((m) => `${m.provider}/${m.id}`),
          find: model(reg),
          findMissing: registry.find("parity-reg", "nope") ?? null,
          hasConfiguredAuth: registry.hasConfiguredAuth(reg),
          authStatus: {
            reg: registry.getProviderAuthStatus("parity-reg"),
            late: registry.getProviderAuthStatus("parity-late"),
            anthropic: registry.getProviderAuthStatus("anthropic"),
            missing: registry.getProviderAuthStatus("parity-missing"),
          },
          displayName: {
            reg: registry.getProviderDisplayName("parity-reg"),
            anthropic: registry.getProviderDisplayName("anthropic"),
            missing: registry.getProviderDisplayName("parity-missing"),
          },
          provider: {
            reg: { id: provider?.id, name: provider?.name, baseUrl: provider?.baseUrl, models: provider?.getModels().map((m) => m.id) },
            anthropic: registry.getProvider("anthropic")?.name ?? null,
            missing: registry.getProvider("parity-missing") ?? null,
          },
          isUsingOAuth: registry.isUsingOAuth(reg),
          error: registry.getError() ?? null,
          apiKeyAndHeaders: await registry.getApiKeyAndHeaders(reg),
          providerAuth: (await registry.getProviderAuth("parity-reg")) ?? null,
          providerAuthMissing: (await registry.getProviderAuth("parity-missing")) ?? null,
          apiKeyForProvider: (await registry.getApiKeyForProvider("parity-reg")) ?? null,
          registeredIds: registry.getRegisteredProviderIds().filter((id) => id.startsWith("parity-")),
          registeredConfig: registry.getRegisteredProviderConfig("parity-reg") ?? null,
          registeredMissing: registry.getRegisteredProviderConfig("parity-missing") ?? null,
          nativeProvider: registry.getRegisteredNativeProvider("parity-reg") ?? null,
          refresh: { aborted: refreshed.aborted, errors: [...refreshed.errors.keys()] },
          late: model(registry.find("parity-late", "late-a")),
        },
        session: {
          members: SESSION_MEMBERS.filter((name) => typeof session[name] === "function"),
          cwdIsContextCwd: session.getCwd() === ctx.cwd,
          sessionDirIsTemp: session.getSessionDir().endsWith("/sessions"),
          sessionFileInDir: (session.getSessionFile() ?? "").startsWith(session.getSessionDir()),
          sessionIdMatchesHeader: session.getSessionId() === header?.id,
          sessionName: session.getSessionName() ?? null,
          header: header && { type: header.type, version: header.version, cwdIsContextCwd: header.cwd === ctx.cwd, forkedFromFixture: (header.parentSession ?? "").endsWith("registry-session.jsonl") },
          leafIsNote: session.getLeafId() === noteId,
          leafEntryIsNote: session.getLeafEntry() === note,
          note: note && { type: note.type, customType: note.customType, data: note.data, parentIsPreviousLeaf: note.parentId !== null },
          entryTypes: session.getEntries().map((entry) => entry.type),
          branchTypes: session.getBranch().map((entry) => entry.type),
          branchFromAbandoned: session.getBranch("e0000006").map((entry) => entry.id),
          childrenOfLabel: session.getChildren("e0000005").map((entry) => entry.id),
          persisted: session.isPersisted?.() ?? null,
          usesDefaultSessionDir: session.usesDefaultSessionDir?.() ?? null,
          labels: { cleared: session.getLabel("e0000003") ?? null, note: session.getLabel("e0000008") ?? null },
          entry: session.getEntry("e0000011"),
          missingEntry: session.getEntry("missing") ?? null,
          contextTypes: session.buildContextEntries().map((entry) => entry.type),
          projection: session.buildSessionProjection().entries.map((entry) => ({ type: entry.sourceEntry.type, roles: entry.messages.map((m) => m.role) })),
          contextMessages: session.buildSessionContext().messages,
          contextThinkingLevel: session.buildSessionContext().thinkingLevel,
          contextModel: session.buildSessionContext().model,
          tree: tree(session.getTree()),
        },
      };
      writeFileSync(args.trim(), `${JSON.stringify(sortedKeys(info), null, 1)}\n`);
      ctx.ui.notify("registry-artifact-written", "info");
    },
  });
}
