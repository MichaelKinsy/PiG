// Drive the installed Pi InMemoryTelemetryContext (memory.ts) metrics with seeded random instrument programs and print the recorded measurements, never a translated oracle.
// The recorder's random scope UUID is printed as SCOPE.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-telemetry/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { InMemoryTelemetryContext } = await import(pathToFileURL(root + "dist/memory.js"));
const { NOOP_TELEMETRY_CONTEXT } = await import(pathToFileURL(root + "dist/noop.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

// {"$undef":true} stands for undefined and {"$num":"NaN"} for a number JSON cannot carry.
const revive = (attributes) => {
	if (attributes === null || attributes === undefined) return undefined;
	const out = {};
	for (const [name, value] of Object.entries(attributes)) out[name] = value !== null && typeof value === "object" && !Array.isArray(value) && value.$undef ? undefined : value;
	return out;
};
const num = (value) => (value !== null && typeof value === "object" ? Number(value.$num) : value);
const results = [];
for (const program of JSON.parse(input)) {
	const telemetry = new InMemoryTelemetryContext();
	const kept = [];
	for (let i = 0; i < program.spans; i++) await telemetry.startSpan({ name: `k${i}` }, (span) => { kept.push(span); });
	for (const step of program.steps) {
		const options = {};
		if (step.description !== undefined) options.description = step.description;
		if (step.unit !== undefined) options.unit = step.unit;
		if (step.boundaries !== undefined) options.boundaries = step.boundaries.map(num);
		const instrument = step.kind === "counter" ? telemetry.counter(step.name, options) : step.kind === "upDown" ? telemetry.upDownCounter(step.name, options) : telemetry.histogram(step.name, options);
		const run = (self) => {
			for (const call of step.calls) {
				if (step.kind !== "histogram") { instrument.add(num(call.value), revive(call.attrs)); continue; }
				switch (call.ctx) {
					case "omit": instrument.record(num(call.value), revive(call.attrs)); break;
					case "noop": instrument.record(num(call.value), revive(call.attrs), NOOP_TELEMETRY_CONTEXT); break;
					case "recorder": instrument.record(num(call.value), revive(call.attrs), telemetry); break;
					case "kept": instrument.record(num(call.value), revive(call.attrs), kept[call.idx % kept.length]); break;
					case "self": instrument.record(num(call.value), revive(call.attrs), self); break;
					default: instrument.record(num(call.value), revive(call.attrs), undefined);
				}
			}
		};
		if (step.inSpan) await telemetry.startSpan({ name: "step" }, (span) => run(span));
		else run(undefined);
	}
	results.push(JSON.parse(JSON.stringify(telemetry.getMetrics()).replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g, "SCOPE")));
}
process.stdout.write(JSON.stringify(results));
