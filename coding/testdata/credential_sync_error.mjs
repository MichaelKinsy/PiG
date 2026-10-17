// Construct the installed Pi's CredentialSynchronizationError (core/model-runtime.ts) for each probe and report its observable members.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { CredentialSynchronizationError } = await import(pathToFileURL(root + "dist/core/model-runtime.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  const error = new CredentialSynchronizationError(probe.provider, probe.operation, probe.credential ?? undefined, { cause: new Error(probe.cause) });
  return {
    message: error.message,
    name: error.name,
    isError: error instanceof Error,
    providerId: error.providerId,
    operation: error.operation,
    credential: error.credential === undefined ? null : JSON.stringify(error.credential),
    cause: error.cause.message,
    string: String(error),
  };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
