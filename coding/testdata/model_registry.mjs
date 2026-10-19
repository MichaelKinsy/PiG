// Drive the installed Pi's ModelRegistry (over a real ModelRuntime on temp auth.json and models.json files) with seeded scenarios: the catalog, availability, auth resolution, provider registration, refresh after a models.json rewrite.
import { readFileSync, realpathSync, mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
// Pi gives a `!command` credential ten seconds (execSync timeout, resolve-config-value.ts); a loaded host must not decide the comparison, so the oracle waits as the Go side does.
const childProcess = (await import("node:child_process")).default;
const { syncBuiltinESMExports } = await import("node:module");
const execSync = childProcess.execSync;
childProcess.execSync = (command, options) => execSync(command, { ...options, timeout: undefined });
syncBuiltinESMExports();
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { ModelRuntime } = await import(pathToFileURL(root + "dist/core/model-runtime.js"));
const { ModelRegistry } = await import(pathToFileURL(root + "dist/core/model-registry.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const project = (model) => model === undefined ? null : { provider: model.provider, id: model.id, name: model.name, api: model.api, baseUrl: model.baseUrl, reasoning: model.reasoning, input: model.input, cost: model.cost, contextWindow: model.contextWindow, maxTokens: model.maxTokens, headers: model.headers ?? null };
const results = [];
for (const scenario of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "model-registry-oracle-"));
  const savedEnv = { ...process.env };
  try {
    for (const key of Object.keys(process.env)) if (/_API_KEY$|_TOKEN$|^AWS_|^GOOGLE_|^AZURE_|^PI_|^CLOUDFLARE|^ANTHROPIC|^OPENAI/.test(key)) delete process.env[key];
    process.env.PI_OFFLINE = "1";
    for (const [key, value] of Object.entries(scenario.env)) process.env[key] = value;
    const authPath = join(dir, "auth.json");
    const modelsPath = join(dir, "models.json");
    if (scenario.auth !== null) writeFileSync(authPath, scenario.auth);
    if (scenario.models !== null) writeFileSync(modelsPath, scenario.models);
    const runtime = await ModelRuntime.create({ authPath, modelsPath });
    const registry = new ModelRegistry(runtime);
    // registerProvider, registerNativeProvider and unregisterProvider start `void this.refresh({ allowNetwork: false })` (model-runtime.ts). That pass and a following awaited refresh race to reach queueAvailabilityRefresh, so Pi's published snapshot depends on Node's I/O completion order. Pig orders them deterministically: a refresh over every provider first waits for the queued registration refresh (D84). The "refresh" op settles Pi's pending unawaited refreshes first, which is that order.
    const pendingRefreshes = new Set();
    const runtimeRefresh = runtime.refresh.bind(runtime);
    runtime.refresh = (options) => {
      const pending = runtimeRefresh(options);
      pendingRefreshes.add(pending);
      pending.then(() => pendingRefreshes.delete(pending), () => pendingRefreshes.delete(pending));
      return pending;
    };
    const find = (ref) => registry.getAll().find((m) => `${m.provider}/${m.id}` === ref);
    const outs = [];
    for (const op of scenario.ops) {
      let out;
      try {
        switch (op.op) {
          case "error": out = (registry.getError() ?? null)?.replaceAll(dir, "<dir>") ?? null; break;
          case "all": out = registry.getAll().map((m) => `${m.provider}/${m.id}`).filter((r) => !op.provider || r.startsWith(op.provider + "/")); break;
          case "available": out = registry.getAvailable().map((m) => `${m.provider}/${m.id}`); break;
          case "virtual": runtime.registerVirtualModel({ provider: op.provider, id: op.ref, name: "Virtual " + op.ref, route: () => { throw new Error("unrouted"); } }); out = null; break;
          case "unvirtual": runtime.unregisterVirtualModel(op.provider, op.ref); out = null; break;
          case "physical": { const [p, ...rest] = op.ref.split("/"); const m = runtime.getPhysicalModel(p, rest.join("/")); out = m ? `${m.provider}/${m.id}` : null; break; }
          case "snapshot": out = runtime.getAvailableSnapshot().map((m) => `${m.provider}/${m.id}`); break;
          case "find": { const [p, ...rest] = op.ref.split("/"); out = project(registry.find(p, rest.join("/"))); break; }
          case "hasAuth": { const m = find(op.ref); out = m ? registry.hasConfiguredAuth(m) : "missing"; break; }
          case "headers": {
            const m = find(op.ref);
            if (!m) { out = "missing"; break; }
            const r = await registry.getApiKeyAndHeaders(m);
            out = { ok: r.ok, apiKey: r.apiKey ?? null, headers: r.headers ? Object.entries(r.headers).sort(([a], [b]) => (a < b ? -1 : 1)) : null, baseUrl: r.baseUrl ?? null, env: r.env ? Object.entries(r.env).sort(([a], [b]) => (a < b ? -1 : 1)) : null, error: r.error ?? null };
            break;
          }
          case "status": { const r = registry.getProviderAuthStatus(op.provider); out = { configured: r.configured, source: r.source ?? null, label: r.label ?? null }; break; }
          case "display": out = registry.getProviderDisplayName(op.provider); break;
          case "oauth": { const m = find(op.ref); out = m ? registry.isUsingOAuth(m) : "missing"; break; }
          case "apiKey": out = (await registry.getApiKeyForProvider(op.provider)) ?? null; break;
          case "register": registry.registerProvider(op.name, op.config); out = null; break;
          case "unregister": registry.unregisterProvider(op.name); out = null; break;
          case "registered": out = [...registry.getRegisteredProviderIds()]; break;
          case "typed": out = runtime.getModelsOfType(op.type, op.provider || undefined).map((m) => `${m.provider}/${m.id}`); break;
          case "typedOne": { const [p, ...rest] = op.ref.split("/"); const m = registry.findOfType(op.type, p, rest.join("/")); out = m ? `${m.provider}/${m.id}` : null; break; }
          case "typedAvailable": out = (await runtime.getAvailableOfType(op.type, op.provider || undefined)).map((m) => `${m.provider}/${m.id}`); break;
          case "compat": {
            const m = find(op.ref);
            if (!m) { out = "missing"; break; }
            const c = runtime.getCompatibilityRequestConfig(m);
            out = { headers: c.headers ? Object.entries(c.headers).sort(([a], [b]) => (a < b ? -1 : 1)) : null, authHeader: c.authHeader };
            break;
          }
          case "provider": { const pr = runtime.getProvider(op.provider); out = pr ? { id: pr.id, name: pr.name, baseUrl: pr.baseUrl ?? null, models: pr.getModels().map((m) => m.id) } : null; break; }
          case "registeredConfig": { const c = runtime.getRegisteredProviderConfig(op.provider); out = c ? { name: c.name ?? null, baseUrl: c.baseUrl ?? null, apiKey: c.apiKey ?? null, authHeader: c.authHeader ?? null, api: c.api ?? null, models: c.models ? c.models.map((m) => m.id) : null } : null; break; }
          case "providers": out = runtime.getProviders().map((p) => p.id); break;
          case "allModels": out = runtime.getAllModels(op.provider || undefined).map((m) => `${m.provider}/${m.id}`); break;
          case "allAvailable": out = (await runtime.getAllAvailable(op.provider || undefined)).map((m) => `${m.provider}/${m.id}`); break;
          case "getAuth": {
            const r = op.via === "registry" ? await registry.getProviderAuth(op.provider) : await runtime.getAuth(op.provider, op.overrides ?? undefined);
            out = r === undefined ? null : { apiKey: r.auth.apiKey ?? null, headers: r.auth.headers ? Object.entries(r.auth.headers).sort(([a], [b]) => (a < b ? -1 : 1)) : null, baseUrl: r.auth.baseUrl ?? null, env: r.env ? Object.entries(r.env).sort(([a], [b]) => (a < b ? -1 : 1)) : null, source: r.source ?? null };
            break;
          }
          case "checkAuth": { const r = await runtime.checkAuth(op.provider); out = r === undefined ? null : { type: r.type, source: r.source ?? null }; break; }
          case "rtHasAuth": out = runtime.hasConfiguredAuth(op.provider); break;
          case "rtOAuth": out = runtime.isUsingOAuth(op.provider); break;
          case "subscription": out = runtime.isUsingSubscription(op.provider); break;
          case "setKey": await runtime.setRuntimeApiKey(op.provider, op.key); out = null; break;
          case "removeKey": await runtime.removeRuntimeApiKey(op.provider); out = null; break;
          case "credentials": out = (await runtime.listCredentials()).map((c) => `${c.providerId}:${c.type}`).sort(); break;
          case "logout": await runtime.logout(op.provider); out = null; break;
          case "rewrite": writeFileSync(modelsPath, op.models); out = null; break;
          case "refresh": { await Promise.allSettled([...pendingRefreshes]); const r = await registry.refresh(); out = { aborted: r.aborted, errors: [...r.errors].map(([k, e]) => [k, String(e.message).replaceAll(dir, "<dir>")]) }; break; }
          default: throw new Error("op " + op.op);
        }
      } catch (error) { out = { thrown: String(error && error.message) }; }
      outs.push(out);
    }
    results.push(outs);
  } finally {
    for (const key of Object.keys(process.env)) delete process.env[key];
    Object.assign(process.env, savedEnv);
    rmSync(dir, { recursive: true, force: true });
  }
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
