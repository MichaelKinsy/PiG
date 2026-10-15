// Drive the installed Pi's ProgramStatusReporter over recorded event sequences, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { ProgramStatusReporter } = await import(pathToFileURL(root + "dist/modes/interactive/program-status-reporter.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((ops) => {
  const reports = [];
  let name = "";
  const terminal = { setProgramStatus: (status) => reports.push(status) };
  const reporter = new ProgramStatusReporter(() => terminal, () => (name === "" ? undefined : name));
  for (const op of ops) {
    switch (op.op) {
      case "event": reporter.handleEvent(op.event); break;
      case "blocked": reporter.setBlocked(op.source, op.status ?? undefined); break;
      case "reset": reporter.reset(); break;
      case "report": reporter.report(); break;
      case "name": name = op.value; break;
    }
  }
  // The app name is the program's own (pi, pig); every other field is compared.
  return reports.map(({ app, ...rest }) => rest);
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
