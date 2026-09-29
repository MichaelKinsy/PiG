// Ports packages/coding-agent/test/suite/regressions/6260-inline-extension-naming.test.ts.
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { DefaultResourceLoader } from "@earendil-works/pi-coding-agent";

const noop = () => {};

export default function (pi) {
  pi.registerCommand("inline-naming", {
    handler: async (args) => {
      const { root, scenario } = JSON.parse(args);
      const cwd = join(root, "project");
      const agentDir = join(root, "agent");
      mkdirSync(cwd, { recursive: true });
      mkdirSync(agentDir, { recursive: true });
      const cases = {
        // Upstream :36: displays bare factories as <inline:N>.
        bare: { factories: [noop, noop], paths: ["<inline:1>", "<inline:2>"] },
        // Upstream :56: displays named wrappers as <inline:name>.
        named: {
          factories: [{ name: "my-provider", factory: noop }, { name: "my-commands", factory: noop }],
          paths: ["<inline:my-provider>", "<inline:my-commands>"],
        },
        // Upstream :79: preserves hidden state for named factories.
        hidden: {
          factories: [{ name: "built-in", factory: noop, hidden: true }],
          paths: ["<inline:built-in>"],
        },
        // Upstream :99: supports mixed bare and named factories.
        mixed: {
          factories: [noop, { name: "named-ext", factory: noop }, noop],
          paths: ["<inline:1>", "<inline:named-ext>", "<inline:3>"],
        },
      };
      const expected = cases[scenario];
      assert.ok(expected, `unknown scenario: ${scenario}`);
      const loader = new DefaultResourceLoader({
        cwd, agentDir, noSkills: true, noPromptTemplates: true, noThemes: true,
        extensionFactories: expected.factories,
      });
      await loader.reload();
      const result = loader.getExtensions();
      assert.equal(result.extensions.length, expected.paths.length);
      assert.deepEqual(result.extensions.map((extension) => extension.path), expected.paths);
      if (scenario === "hidden") assert.equal(result.extensions[0].hidden, true);
    },
  });
}
