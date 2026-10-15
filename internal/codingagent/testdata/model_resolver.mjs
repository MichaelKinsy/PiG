// Run the installed Pi's findExactModelReferenceMatch, parseModelPattern, resolveModelScopeFromModels and resolveCliModel (core/model-resolver.ts), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { findExactModelReferenceMatch, parseModelPattern, resolveModelScopeFromModels, resolveCliModel } = await import(pathToFileURL(root + "dist/core/model-resolver.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { catalogs, probes } = JSON.parse(input);
const key = (m) => (m ? [m.provider, m.id, m.name, !!m.reasoning] : null);
const results = probes.map((probe) => {
  const catalog = catalogs[probe.catalog];
  const models = catalog.models.map((m) => ({ ...m }));
  try {
    switch (probe.kind) {
      case "exact": return { model: key(findExactModelReferenceMatch(probe.pattern, models)) };
      case "parse": {
        const r = parseModelPattern(probe.pattern, models, { allowInvalidThinkingLevelFallback: probe.allowFallback });
        return { model: key(r.model), thinking: r.thinkingLevel ?? "", warning: r.warning ?? "" };
      }
      case "scope": {
        const r = resolveModelScopeFromModels(probe.patterns, models);
        return { scoped: r.scopedModels.map((s) => [key(s.model), s.thinkingLevel ?? ""]), diagnostics: r.diagnostics.map((d) => [d.code, d.message, d.pattern]) };
      }
      case "cli": {
        const runtime = { getModels: () => models, hasConfiguredAuth: (p) => catalog.authed.includes(p) };
        const r = resolveCliModel({ cliProvider: probe.provider || undefined, cliModel: probe.model || undefined, cliThinking: probe.thinking || undefined, modelRuntime: runtime });
        return { model: key(r.model), thinking: r.thinkingLevel ?? "", warning: r.warning ?? "", error: r.error ?? "" };
      }
    }
  } catch (error) { return { threw: String(error) }; }
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
