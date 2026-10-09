// Run the installed Pi's parseAuthCommand and validateAuthCommandArgs (cli/auth-command.ts), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const load = (p) => import(pathToFileURL(root + p));
const { parseAuthCommand, validateAuthCommandArgs, getAuthCredential } = await load("dist/cli/auth-command.js");
const { parseArgs } = await load("dist/cli/args.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  if (test.credential) return { credential: getAuthCredential(test.credential) ?? "" };
  const out = {};
  try {
    const command = parseAuthCommand(test.args);
    if (!command) return { none: true };
    out.command = command;
    try { out.target = validateAuthCommandArgs(parseArgs(command.args), command.kind); } catch (e) { out.validateError = e.message; }
  } catch (e) { out.parseError = e.message; }
  return out;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
