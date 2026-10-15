// Call the installed Pi's getPackageDir (config.ts) with PI_PACKAGE_DIR set to each probe value and HOME set to the given directory, and report the result.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { getPackageDir, getAgentDir } = await import(pathToFileURL(root + "dist/config.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((value) => {
  process.env.PI_PACKAGE_DIR = value;
  process.env.PI_CODING_AGENT_DIR = value;
  let dir = null, agent = null;
  try { dir = getPackageDir(); } catch (error) { return { threw: true }; }
  try { agent = getAgentDir(); } catch (error) { return { threw: true }; }
  return { dir, agent };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
