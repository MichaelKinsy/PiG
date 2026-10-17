// Run Pi's packages/tui/src/wheel-scroll.ts WheelScrollAccelerator over scripts from stdin:
// [{lines, accelerate, ops: [{set} | {dir, now}]}] -> [[number]], the result of each {dir, now} op. A lines value is "auto", a number, "NaN", "Infinity" or "-Infinity".
// usage: node wheel_scroll.mjs <path to packages/tui/src/wheel-scroll.ts>
import { pathToFileURL } from "node:url";
const { WheelScrollAccelerator } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const lines = (v) => (v === "NaN" ? Number.NaN : v === "Infinity" ? Infinity : v === "-Infinity" ? -Infinity : v);
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((script) => {
			const accelerator = new WheelScrollAccelerator(lines(script.lines), script.accelerate);
			const out = [];
			for (const op of script.ops) {
				if (op.set !== undefined) accelerator.setLines(lines(op.set));
				else out.push(accelerator.next(op.dir, op.now));
			}
			return out;
		}),
	),
);
