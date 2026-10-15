// Create the installed Pi's agent session services for each (cwd, agentDir) probe and report where it anchored them: the resolved cwd, the resolved agent directory, and the credential file it created. Usage: node agent_session_services.mjs <pi version> <sandbox>; the probes arrive on stdin with {S} standing for the sandbox.
import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const sandbox = process.argv[3];
process.env.HOME = join(sandbox, "home");
process.env.PI_CODING_AGENT_DIR = join(sandbox, "default-agent");
mkdirSync(process.env.HOME, { recursive: true });
process.chdir(sandbox);
const { createAgentSessionServices } = await import(pathToFileURL(root + "dist/core/agent-session-services.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const fill = (text) => (text === null ? undefined : text.replaceAll("{S}", sandbox));
const strip = (text) => text.replaceAll(sandbox, "{S}");
const results = [];
for (const probe of JSON.parse(input)) {
  try {
    const services = await createAgentSessionServices({ cwd: fill(probe.cwd), agentDir: fill(probe.agentDir) });
    results.push({ cwd: strip(services.cwd), agentDir: strip(services.agentDir), auth: existsSync(join(services.agentDir, "auth.json")), error: null });
  } catch (error) {
    results.push({ cwd: null, agentDir: null, auth: false, error: strip(error instanceof Error ? error.message : String(error)) });
  }
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
