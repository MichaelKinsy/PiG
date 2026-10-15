import { appendFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

// Each resources_discover call appends its reason to PIG_TEST_DISCOVER_LOG and contributes one prompt template.
export default function (pi) {
  pi.on("resources_discover", (event) => {
    appendFileSync(process.env.PIG_TEST_DISCOVER_LOG, `${event.reason}\n`);
    return { promptPaths: [join(here, "prompts")] };
  });
}
