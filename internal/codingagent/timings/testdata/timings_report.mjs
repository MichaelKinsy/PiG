// Drive the installed Pi's timings.ts (PI_TIMING=1, a scripted Date.now, a captured console.error) through each scenario, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.PI_TIMING = "1";
let clock = 0;
Date.now = () => clock;
let written = "";
console.error = (...args) => {
	written += `${args.join(" ")}\n`;
};
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const reports = [];
for (const [index, scenario] of JSON.parse(input).entries()) {
	// A fresh module instance per scenario: its namespaces start empty, as in a new process.
	const { resetTimings, time, printTimings } = await import(`${pathToFileURL(root + "dist/core/timings.js")}?scenario=${index}`);
	clock = 1000;
	written = "";
	for (const step of scenario) {
		if (step.op === "advance") clock += step.ms;
		else if (step.op === "reset") step.namespace ? resetTimings(step.namespace) : resetTimings();
		else if (step.op === "time") step.namespace ? time(step.label, step.namespace) : time(step.label);
		else if (step.op === "print") printTimings();
	}
	reports.push(written);
}
process.stdout.write(JSON.stringify(reports));
