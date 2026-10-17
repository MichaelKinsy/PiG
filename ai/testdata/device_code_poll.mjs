// Run Pi's pollOAuthDeviceCodeFlow (packages/ai/src/auth/oauth/device-code.ts) on a virtual clock.
// stdin: [{intervalSeconds, expiresInSeconds, waitBeforeFirstPoll, abortAt, script}]; numbers may be the strings "NaN", "Infinity" and "-Infinity"; undefined members are absent.
// script: one entry per poll call, {cost, status, intervalSeconds, message, value} (cost = virtual ms the poll takes); a poll beyond the script throws "exhausted".
// stdout: [{events: [["poll", t] | ["sleep", ms, t]], result: {value} | {err}}] with t the virtual ms since the start.
// Date.now is virtual and a timer fires after Node's coerced delay (NaN, <1 or >2^31-1 waits 1ms; fractions truncate); abortAt (ms since the start, or -1 for aborted at the start) aborts the signal when the clock reaches it.
// usage: node device_code_poll.mjs <path to device-code.ts>
import { pathToFileURL } from "node:url";
const { pollOAuthDeviceCodeFlow } = await import(pathToFileURL(process.argv[2]).href);
const START = 1_700_000_000_000;
const num = (v) => (typeof v === "string" ? Number(v) : v);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const realSetTimeout = globalThis.setTimeout;
const realClearTimeout = globalThis.clearTimeout;
const flush = () => new Promise((resolve) => setImmediate(resolve));
const results = [];
for (const probe of JSON.parse(input)) {
	let now = START;
	Date.now = () => now;
	const timers = [];
	const events = [];
	let aborted = false;
	const controller = new AbortController();
	const abortTime = probe.abortAt === undefined ? undefined : probe.abortAt === -1 ? START : START + probe.abortAt;
	if (probe.abortAt === -1) controller.abort();
	// Advance the virtual clock to t, firing the timers and the abort that fall on the way, in time order.
	const advanceTo = async (t) => {
		for (;;) {
			timers.sort((a, b) => a.at - b.at);
			const next = timers[0];
			const abortDue = abortTime !== undefined && !aborted && abortTime <= t;
			if (abortDue && (!next || abortTime <= next.at)) {
				now = Math.max(now, abortTime);
				aborted = true;
				controller.abort();
				await flush();
				continue;
			}
			if (next && next.at <= t) {
				timers.shift();
				now = Math.max(now, next.at);
				next.fn();
				await flush();
				continue;
			}
			now = Math.max(now, t);
			return;
		}
	};
	globalThis.setTimeout = (fn, ms) => {
		const coerced = Number.isNaN(ms) || !(ms >= 1 && ms <= 2147483647) ? 1 : Math.trunc(ms);
		events.push(["sleep", ms === Infinity ? "Infinity" : Number.isNaN(ms) ? "NaN" : ms, now - START]);
		const timer = { at: now + coerced, fn };
		timers.push(timer);
		return timer;
	};
	globalThis.clearTimeout = (timer) => {
		const index = timers.indexOf(timer);
		if (index >= 0) timers.splice(index, 1);
	};
	let calls = 0;
	const poll = async () => {
		events.push(["poll", now - START]);
		const step = probe.script[calls++];
		if (!step) throw new Error("exhausted");
		await advanceTo(now + step.cost);
		if (step.status === "throw") throw new Error(step.message);
		if (step.status === "complete") return { status: "complete", value: step.value };
		if (step.status === "failed") return { status: "failed", message: step.message };
		if (step.status === "slow_down") {
			return step.intervalSeconds === undefined ? { status: "slow_down" } : { status: "slow_down", intervalSeconds: num(step.intervalSeconds) };
		}
		return { status: "pending" };
	};
	const options = { poll, signal: controller.signal };
	if (probe.intervalSeconds !== undefined) options.intervalSeconds = num(probe.intervalSeconds);
	if (probe.expiresInSeconds !== undefined) options.expiresInSeconds = num(probe.expiresInSeconds);
	if (probe.waitBeforeFirstPoll) options.waitBeforeFirstPoll = true;
	let settled;
	const run = pollOAuthDeviceCodeFlow(options).then(
		(value) => (settled = { value }),
		(error) => (settled = { err: error.message }),
	);
	for (let step = 0; step < 100000 && settled === undefined; step++) {
		await flush();
		if (settled !== undefined) break;
		timers.sort((a, b) => a.at - b.at);
		const candidates = [];
		if (timers.length) candidates.push(timers[0].at);
		if (abortTime !== undefined && !aborted) candidates.push(abortTime);
		if (!candidates.length) break;
		await advanceTo(Math.min(...candidates));
	}
	await run;
	results.push({ events, result: settled ?? { err: "stuck" } });
}
globalThis.setTimeout = realSetTimeout;
globalThis.clearTimeout = realClearTimeout;
process.stdout.write(JSON.stringify(results));
