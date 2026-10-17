// Run the installed Pi's copyToClipboard (utils/clipboard.ts) on Linux with scripted clipboard tools on PATH and a scripted environment, and report the OSC 52 bytes written to stdout, the error thrown, and the file each tool recorded.
import { readFileSync, realpathSync, existsSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { copyToClipboard } = await import(pathToFileURL(root + "dist/utils/clipboard.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const write = process.stdout.write.bind(process.stdout);
const managed = ["DISPLAY", "WAYLAND_DISPLAY", "TERMUX_VERSION", "SSH_CONNECTION", "SSH_CLIENT", "MOSH_CONNECTION", "WSL_DISTRO_NAME", "WSLENV", "WT_SESSION", "REC", "PATH"];
const results = [];
for (const scenario of JSON.parse(input)) {
  for (const key of managed) delete process.env[key];
  Object.assign(process.env, scenario.env);
  let written = "";
  process.stdout.write = (chunk) => { written += chunk; return true; };
  let thrown = null;
  try { await copyToClipboard(scenario.text); } catch (error) { thrown = String(error && error.message); }
  process.stdout.write = write;
  results.push({ stdout: written, thrown, recorded: existsSync(scenario.env.REC) ? readFileSync(scenario.env.REC, "utf8") : null });
}
write(JSON.stringify(results), () => process.exit(0));
