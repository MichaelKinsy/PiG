// The effective pin's commit and this package's directory, for scripts that must run without node_modules.
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const sdk = resolve(dirname(fileURLToPath(import.meta.url)), "..");

// The override `make` exports (PIG_UPSTREAM_COMMIT), else the committed pin.
export function pinnedCommit() {
  if (process.env.PIG_UPSTREAM_COMMIT) return process.env.PIG_UPSTREAM_COMMIT;
  const source = readFileSync(resolve(sdk, "../../internal/coding/pigversion/pigversion.go"), "utf8");
  return source.match(/const UpstreamCommit = "([0-9a-f]{40})"/)?.[1];
}
