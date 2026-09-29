import {writeFileSync} from "node:fs";

// Copied into a test-owned temporary directory before loading.
export default function (pi) {
  pi.registerCommand("load-skills", {
    description: "Load skills",
    getArgumentCompletions: (prefix) => {
      writeFileSync(new URL("received-prefix.txt", import.meta.url), prefix);
      return "not-an-array";
    },
    handler: async () => {},
  });
}
