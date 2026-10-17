// Drive the installed Pi's emitProjectTrustEvent with seeded extensions and handlers: the order handlers run in, which result wins (first yes/no, undecided falls through), and the error each failing handler reports (extension path, event type, message, whether a stack exists).
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { emitProjectTrustEvent } = await import(pathToFileURL(root + "dist/core/extensions/runner.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const scenario of JSON.parse(input)) {
  const calls = [];
  const extensions = scenario.extensions.map((ext, e) => ({
    path: ext.path,
    handlers: new Map([["project_trust", ext.handlers.map((h, i) => async (event, ctx) => {
      calls.push(`${e}.${i}:${event.cwd}:${ctx.cwd}`);
      switch (h.kind) {
        case "yes": case "no": case "undecided": {
          const result = { trusted: h.kind };
          if (h.remember !== null) result.remember = h.remember;
          return result;
        }
        case "throwError": throw new Error(h.message);
        case "throwString": throw h.message;
        case "undefined": return undefined;
      }
    })]]),
  }));
  const { result, errors } = await emitProjectTrustEvent({ extensions }, { type: "project_trust", cwd: scenario.cwd }, { cwd: scenario.cwd });
  results.push({
    calls,
    result: result === undefined ? null : { trusted: result.trusted, remember: result.remember ?? null },
    errors: errors.map((e) => ({ extensionPath: e.extensionPath, event: e.event, error: e.error, hasStack: typeof e.stack === "string" && e.stack !== "" })),
  });
}
process.stdout.write(JSON.stringify(results));
