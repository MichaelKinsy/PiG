import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

export default function (pi: any) {
  pi.on("resources_discover", () => ({
    skillPaths: [join(here, "skills")],
    promptPaths: [join(here, "prompts")],
  }));
}
