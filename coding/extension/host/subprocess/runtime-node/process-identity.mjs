import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Ports packages/coding-agent/src/cli/setup.ts: setupCli.
/**
 * Give this process the identity a Pi extension sees in Pi's own process.
 *
 * Pi's setupCli (cli/setup.ts) titles the process "pi" and silences process
 * warnings; PiG's setupCli already exports PI_CODING_AGENT and AI_AGENT, which
 * this process inherits. In Pi, process.argv[1] is Pi's CLI entry and the
 * arguments after it are the ones Pi was started with, so an extension can
 * start the harness again with `process.execPath process.argv[1] ...`
 * (@henryqw/pi-subagent, pi-subagents). Here argv[1] is harness/cli.mjs,
 * which runs the PiG binary that started this process, and the arguments are
 * PiG's own, read from the file the host names (harness_identity.go).
 */
export function adoptPiProcessIdentity() {
  reportTitle("pi");
  process.emitWarning = () => {};
  const argvFile = process.env.PIG_HARNESS_ARGV_FILE;
  delete process.env.PIG_HARNESS_ARGV_FILE;
  const args = argvFile ? JSON.parse(readFileSync(argvFile, "utf8")) : [];
  process.argv = [process.argv[0], fileURLToPath(new URL("./harness/cli.mjs", import.meta.url)), ...args];
}

// process.title reads as Pi's title without renaming the process: the
// operating system keeps showing this process's command line (node, the
// runtime and each extension's entry), which is how PiG's process inspection
// finds an extension's process. A title an extension sets still renames it.
function reportTitle(initial) {
  const native = Object.getOwnPropertyDescriptor(process, "title");
  let title = initial;
  Object.defineProperty(process, "title", {
    configurable: true,
    enumerable: native?.enumerable ?? true,
    get: () => title,
    set: (value) => {
      native?.set?.call(process, value);
      title = native?.get ? native.get.call(process) : String(value);
    },
  });
}
