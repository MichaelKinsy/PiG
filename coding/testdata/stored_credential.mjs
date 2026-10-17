// Run the installed Pi's readStoredCredential (core/auth-storage.ts) over auth.json files the test wrote, with HOME and the agent directory pointed at the test directory, and report the JSON of what it returns.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { readStoredCredential } = await import(pathToFileURL(root + "dist/core/auth-storage.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const probes = JSON.parse(input);
const results = probes.map((probe) => {
  let value;
  try {
    value = probe.path === null ? readStoredCredential(probe.provider) : readStoredCredential(probe.provider, probe.path);
  } catch (error) {
    return { threw: true };
  }
  return value === undefined ? { absent: true } : { truthy: !!value, json: JSON.stringify(value) ?? null };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
