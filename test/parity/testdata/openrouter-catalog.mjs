import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { buildOpenRouterCatalog } from "../../../.upstream/current/packages/ai/scripts/openrouter-catalog.ts";

// Both sides print one line per case with object keys sorted so only values are compared.
function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  return value;
}

if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./cmd/gen-models", "-run", "^TestOpenRouterCatalogParity$", "-count=1", "-v"], { encoding: "utf8", env: { ...process.env, PIG_PARITY_PROBE: "1" } });
  process.stdout.write(output.split("\n").filter((line) => line.startsWith("OPENROUTER_CATALOG ")).join("\n") + "\n");
} else for (const { listed, imageListed, decisionListed } of JSON.parse(readFileSync(new URL("./openrouter-catalog.json", import.meta.url), "utf8"))) {
  console.log("OPENROUTER_CATALOG " + JSON.stringify(canonical(buildOpenRouterCatalog(listed, imageListed, decisionListed))));
}
