import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { parseOpenRouterImageModels } from "../../../.upstream/v0.87.1/packages/ai/scripts/generate-image-models.ts";

if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./cmd/gen-image-models", "-run", "^TestOpenRouterImageCatalogParity$", "-count=1", "-v"], { encoding: "utf8", env: { ...process.env, PIG_PARITY_PROBE: "1" } });
  process.stdout.write(output.split("\n").filter(line => line.startsWith("IMAGE_PARSE ")).join("\n") + "\n");
} else for (const { payload, strict } of JSON.parse(readFileSync(new URL("./image-model-data.json", import.meta.url), "utf8"))) {
  let result;
  try { result = { models: parseOpenRouterImageModels(payload, strict) }; }
  catch (error) { result = { error: error.message }; }
  console.log("IMAGE_PARSE " + JSON.stringify(result));
}
