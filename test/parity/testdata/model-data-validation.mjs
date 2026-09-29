import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createModelDataManifest, validateGeneratedModelData } from "../../../.upstream/v0.87.1/packages/ai/scripts/model-data.ts";

const repository = process.cwd();
const temp = mkdtempSync(join(tmpdir(), "model-data-parity-"));
const binary = join(temp, process.platform === "win32" ? "check-model-data.exe" : "check-model-data");
try {
  if (process.argv[2] === "pig") execFileSync("go", ["build", "-o", binary, "./cmd/check-model-data"], { cwd: repository });
  for (const name of ["valid", "id", "provider", "api", "api-group", "duplicate", "stale-hash", "schema", "stamp", "timestamp", "legacy-date", "rollover-date", "bad-json", "missing-manifest", "missing-data", "directory-file", "shard"]) {
    const root = join(temp, name);
    const dir = join(root, "src/providers/data");
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(root, "src/models.generated.ts"), 'import { TEST_PROVIDER_MODELS } from "./providers/test-provider.models.ts";\n');
    writeFileSync(join(root, "src/providers/test-provider.models.ts"), "export {};\n");
    const structure = { "test-provider": { "model-a": "openai-completions" } };
    const model = { id: "model-a", name: "Model A", api: "openai-completions", provider: "test-provider", baseUrl: "https://example.test/v1", reasoning: false, input: ["text"], cost: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
    if (["id", "provider", "api"].includes(name)) model[name] = "wrong-" + name;
    const values = { "model-a": model };
    const groups = name === "api-group" ? { "anthropic-messages": values } : { "openai-completions": values };
    if (name === "duplicate") groups["anthropic-messages"] = values;
    const content = JSON.stringify(groups) + "\n";
    const manifest = createModelDataManifest(structure, { "test-provider.json": content }, "2026-07-23T10:00:00.000Z");
    if (name === "schema") manifest.schemaVersion++;
    if (name === "stamp") manifest.structureHash = "stale";
    if (name === "timestamp") manifest.generatedAt = "invalid";
    if (name === "legacy-date") manifest.generatedAt = "0";
    if (name === "rollover-date") manifest.generatedAt = "2026-02-30T10:00:00.000Z";
    writeFileSync(join(dir, "test-provider.json"), content + (name === "stale-hash" ? " " : ""));
    writeFileSync(join(dir, ".manifest.json"), JSON.stringify(manifest) + "\n");
    if (name === "shard") writeFileSync(join(root, "src/models.generated.ts"), 'import { TEST_PROVIDER_MODELS } from "./providers/test-provider.models.ts";\nimport { MISSING_MODELS } from "./providers/missing.models.ts";\n');
    if (name === "bad-json") writeFileSync(join(dir, "test-provider.json"), '{"openai-completions":');
    if (name === "missing-manifest") rmSync(join(dir, ".manifest.json"));
    if (name === "missing-data") rmSync(dir, { recursive: true });
    if (name === "directory-file") { rmSync(join(dir, "test-provider.json")); mkdirSync(join(dir, "test-provider.json")); }
    let result;
    if (process.argv[2] === "pig") {
      try { result = { status: 0, output: execFileSync(binary, ["-root", "."], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }) }; }
      catch (error) { result = { status: error.status, output: error.stderr }; }
    } else {
      process.chdir(root);
      try { validateGeneratedModelData("."); result = { status: 0, output: "Generated model data is valid.\n" }; }
      catch (error) { result = { status: 1, output: error.message + "\n\nModel data is missing or stale. Run `npm run hydrate:model-data` from the repository root.\n" }; }
      finally { process.chdir(repository); }
    }
    console.log(JSON.stringify({ name, ...result }));
  }
} finally { process.chdir(repository); rmSync(temp, { recursive: true, force: true }); }
