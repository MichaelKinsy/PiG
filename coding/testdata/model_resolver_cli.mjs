// Run the installed Pi's resolveCliModel and resolveModelScopeWithDiagnostics (core/model-resolver.ts) over a real ModelRuntime with the built-in catalog and in-memory credentials, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { ModelRuntime, resolveCliModel } = await import(pathToFileURL(root + "dist/index.js"));
const { resolveModelScopeWithDiagnostics } = await import(pathToFileURL(root + "dist/core/model-resolver.js"));
const { AuthStorage } = await import(pathToFileURL(root + "dist/core/auth-storage.js"));
const { InMemoryModelsStore } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/models-store.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.env.PI_OFFLINE = "1";
const ref = (m) => (m ? `${m.provider}/${m.id}` : null);
const { probes } = JSON.parse(input);
const results = [];
for (const authed of [["anthropic", "openai"], ["openai"], []]) {
  const credentials = Object.fromEntries(authed.map((p) => [p, { type: "api_key", key: "k-" + p }]));
  const runtime = await ModelRuntime.create({ credentials: AuthStorage.inMemory(credentials), modelsStore: new InMemoryModelsStore(), modelsPath: null, allowModelNetwork: false });
  const out = [];
  for (const probe of probes) {
    try {
      if (probe.kind === "cli") {
        const r = resolveCliModel({ cliProvider: probe.provider || undefined, cliModel: probe.model || undefined, cliThinking: probe.thinking || undefined, modelRuntime: runtime });
        out.push({ model: ref(r.model), thinking: r.thinkingLevel ?? null, warning: r.warning ?? null, error: r.error ?? null });
      } else {
        const r = await resolveModelScopeWithDiagnostics(probe.patterns, runtime);
        out.push({ scoped: r.scopedModels.map((s) => [ref(s.model), s.thinkingLevel ?? null]), diagnostics: r.diagnostics.map((d) => [d.code, d.message, d.pattern]) });
      }
    } catch (error) { out.push({ threw: String(error && error.message) }); }
  }
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
