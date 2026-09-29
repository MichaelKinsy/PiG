// Pi 0.87.1 core/session-manager.ts:1801-1807 and model-registry.ts:34-171.
import { writeFileSync } from "node:fs";
import { SessionManager, ModelRegistry } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.registerCommand("imported-registry-session", {
    description: "Exercise independently constructed Pi classes",
    handler: async (args, ctx) => {
      const s = SessionManager.inMemory(ctx.cwd);
      const note = s.appendCustomEntry("note", { value: 7 });
      const user = s.appendMessage({ role: "user", content: "original", timestamp: 123 });
      s.appendLabelChange(user, "chosen");
      s.appendContextEdit(user, { content: "edited" });
      const model = { id: "model", provider: "fixture" };
      const models = [model];
      let auth;
      const r = new ModelRegistry({
        getModels: () => models,
        getAvailableSnapshot: () => models,
        getModel: () => model,
        getProvider: () => ({ name: "Fixture" }),
        getAuth: async () => { if (auth instanceof Error) throw auth; return auth; },
        getCompatibilityRequestConfig: () => ({ authHeader: true }),
      });
      const all = r.getAll(); all.pop();
      const absent = await r.getApiKeyAndHeaders(model);
      auth = { auth: { apiKey: "key", baseUrl: "https://auth.invalid" }, env: { REGION: "test" } };
      const resolved = await r.getApiKeyAndHeaders(model);
      auth = new Error("outer", { cause: new Error("authHeader requires a resolved API key") });
      const failure = await r.getApiKeyAndHeaders(model);
      const out = {
        dir: ctx.sessionManager.getSessionDir(),
        cwdMatches: s.getCwd() === ctx.cwd,
        persisted: s.isPersisted(),
        types: s.getEntries().map(e => e.type),
        note: s.getEntry(note).data,
        label: s.getLabel(user),
        messages: s.buildSessionContext().messages,
        branch: s.getBranch(note).map(e => e.type),
        roots: s.getTree().map(n => n.entry.type),
        models: r.getAll(), available: r.getAvailable(), sameModel: r.find("fixture", "model") === model,
        displayName: r.getProviderDisplayName("fixture"), absent, resolved, failure,
        missingKey: (await r.getApiKeyForProvider("fixture")) ?? null,
      };
      writeFileSync(args.trim(), JSON.stringify(out, null, 1) + "\n");
    },
  });
}
