// Records upstream's report.ts behavior for evals_oracle_test.go.
//
// Run from a directory holding .upstream/v1.0.0/packages/evals/src/{plan,report}.ts and an npm install of
// @vitest-evals/core@0.15.0 (Node 24 strips the types):
//   node oracle.ts > oracle.json
import { mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { formatEvalComparisonReport, readTaskObservation, summarizeEvalObservations } from "./report.ts";

let seed = 20260514;
function random(): number {
	seed = (seed * 1103515245 + 12345) % 2147483648;
	return seed / 2147483648;
}
function pick<T>(values: readonly T[]): T {
	return values[Math.floor(random() * values.length)];
}

const evalSets = ["tool access", "Äpfel", "apple", "Zebra", "beta", "alpha"];
const caseIds = ["create", "Create", "edit", "ändern"];
const models = ["fixture/model", "fixture/Model", "other/model"];
const variants = ["without_docs", "with_docs"] as const;
const outcomes = ["scored", "scored", "scored", "scored", "scored", "scored", "scored", "scored", "unscored", "skipped", "pending", "errored"] as const;
const metricNames = ["inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens", "totalTokens", "toolCalls", "totalMs", "estimatedCostUsd"] as const;

function metricValue(name: string): number {
	if (name === "estimatedCostUsd") return pick([0, 0.01, 0.0125, 0.00005, 0.3, 1.23456]);
	if (name === "totalMs") return pick([0, 999.5, 1000, 1234.25, 80, 15.05]);
	return pick([0, 1, 2, 3, 100, 120, 150.5, 333]);
}

const summaries = [];
for (let index = 0; index < 100; index++) {
	const expected = [];
	const observations = [];
	const groups = 1 + Math.floor(random() * 4);
	for (let group = 0; group < groups; group++) {
		const identity = { evalSet: pick(evalSets), caseId: pick(caseIds), model: pick(models), runNumber: 1 + Math.floor(random() * 3) };
		for (const variant of variants) {
			const designed = random() < 0.95 ? 1 : pick([0, 2]);
			for (let n = 0; n < designed; n++) expected.push({ ...identity, variant });
			const observed = random() < 0.95 ? designed : pick([0, 1, 2]);
			for (let n = 0; n < observed; n++) {
				const observation: Record<string, unknown> = { ...identity, variant };
				for (const name of metricNames) if (random() < 0.8) observation[name] = metricValue(name);
				const outcome = pick(outcomes);
				observation.outcome = outcome;
				if (outcome === "scored") observation.score = pick([0, 0.5, 1, 1, 0.25, 0.75]);
				observations.push(observation);
			}
		}
	}
	const report = summarizeEvalObservations(`digest-${index}`, expected as never, observations as never);
	summaries.push({ expected, observations, report, formatted: formatEvalComparisonReport(report) });
}

{
	// Two cases whose outcomes flip between runs: flaky is reported once.
	const expected = [];
	const observations = [];
	for (const caseId of ["create", "edit"]) {
		for (const runNumber of [1, 2]) {
			for (const variant of variants) {
				expected.push({ evalSet: "flaky set", caseId, model: "fixture/model", runNumber, variant });
				observations.push({ evalSet: "flaky set", caseId, model: "fixture/model", runNumber, variant, outcome: "scored", score: runNumber === 1 ? 1 : 0 });
			}
		}
	}
	const report = summarizeEvalObservations("digest-flaky", expected as never, observations as never);
	summaries.push({ expected, observations, report, formatted: formatEvalComparisonReport(report) });
}

const task = {
	file: "evals/example.docs.eval.ts",
	fullName: "Example workflow > handles the case",
	evalSet: "Example workflow",
	caseId: "handles the case",
	variant: "with_docs",
	model: "fixture/model",
	runNumber: 2,
};
const run = {
	output: { ok: true },
	session: { events: [{ type: "message", role: "user", content: "prompt" }] },
	usage: { provider: "fixture", model: "model", inputTokens: 10, outputTokens: 5, totalTokens: 15, toolCalls: 1, metadata: { cacheReadTokens: 2, cacheWriteTokens: 3, estimatedCostUsd: 0.01 } },
	timings: { totalMs: 1234 },
	artifacts: { runId: "run-1", piSessionJsonl: '{"type":"session"}\n' },
	errors: [],
};
const evalMeta = { avgScore: 0.5, scores: [{ name: "Judge", score: 0.5 }], thresholdFailed: false };
function assertion(overrides: Record<string, unknown> = {}) {
	return { ancestorTitles: [task.evalSet], fullName: `${task.evalSet} ${task.caseId}`, status: "passed", title: task.caseId, failureMessages: [], meta: { eval: evalMeta, harness: { name: "with_docs", run } }, ...overrides };
}
function report(assertions: unknown[], overrides: Record<string, unknown> = {}) {
	return { numFailedTests: 0, numPassedTests: 1, numPendingTests: 0, numTodoTests: 0, numTotalTests: 1, startTime: 0, success: true, testResults: [{ message: "", name: "/repo/packages/evals/evals/example.docs.eval.ts", status: "passed", assertionResults: assertions }], ...overrides };
}
const withRun = (patch: Record<string, unknown>) => ({ eval: evalMeta, harness: { name: "with_docs", run: { ...run, ...patch } } });
const reports: Record<string, string> = {
	scored: JSON.stringify(report([assertion()])),
	"harness only passed": JSON.stringify(report([assertion({ meta: { harness: { run } } })])),
	"harness only failed": JSON.stringify(report([assertion({ status: "failed", meta: { harness: { run } } })])),
	"harness without run": JSON.stringify(report([assertion({ meta: { harness: { name: "x" } } })])),
	"eval only": JSON.stringify(report([assertion({ meta: { eval: evalMeta } })])),
	"null eval": JSON.stringify(report([assertion({ meta: { eval: null, harness: { run } } })])),
	"strict meta key": JSON.stringify(report([assertion({ meta: { eval: evalMeta, harness: { run }, extra: 1 } })])),
	"strict run key": JSON.stringify(report([assertion({ meta: withRun({ extra: 1 }) })])),
	"bad event": JSON.stringify(report([assertion({ meta: withRun({ session: { events: [{ type: "message", role: "tool" }] } }) })])),
	"bad tool call": JSON.stringify(report([assertion({ meta: { eval: { ...evalMeta, toolCalls: [{ name: "x", status: "ok", error: { message: "m" } }] }, harness: { run } } })])),
	"tool calls": JSON.stringify(report([assertion({ meta: { eval: { ...evalMeta, toolCalls: [{ name: "x", status: "error", error: { message: "m", code: 3 } }] }, harness: { run } } })])),
	"score above one": JSON.stringify(report([assertion({ meta: { eval: { avgScore: 1.5 }, harness: { run } } })])),
	"null score": JSON.stringify(report([assertion({ meta: { eval: { avgScore: null }, harness: { run } } })])),
	"string metric": JSON.stringify(report([assertion({ meta: withRun({ usage: { ...run.usage, metadata: { cacheReadTokens: "2" } } }) })])),
	"negative metric": JSON.stringify(report([assertion({ meta: withRun({ usage: { ...run.usage, totalTokens: -1 } }) })])),
	"null metadata metric": JSON.stringify(report([assertion({ meta: withRun({ usage: { ...run.usage, metadata: { estimatedCostUsd: null } } }) })])),
	"empty provider": JSON.stringify(report([assertion({ meta: withRun({ usage: { ...run.usage, provider: "" } }) })])),
	"run errors": JSON.stringify(report([assertion({ meta: withRun({ errors: [{ message: "boom" }] }) })])),
	"non-object error": JSON.stringify(report([assertion({ meta: withRun({ errors: ["boom"] }) })])),
	"number artifact": JSON.stringify(report([assertion({ meta: withRun({ artifacts: { piSessionJsonl: 3 } }) })])),
	"unicode identity": JSON.stringify(report([assertion({ meta: withRun({ artifacts: { piSessionJsonl: "x\u2028<&>" } }) })])),
	"wrong full name": JSON.stringify(report([assertion({ fullName: "other" })])),
	"two assertions": JSON.stringify(report([assertion(), assertion()])),
	"todo": JSON.stringify(report([assertion({ status: "todo", meta: {} })])),
	"disabled with meta": JSON.stringify(report([assertion({ status: "disabled" })])),
	"pending": JSON.stringify(report([assertion({ status: "pending", meta: undefined })])),
	"missing success": JSON.stringify((({ success, ...rest }) => rest)(report([assertion()]))),
	"null ancestor titles": JSON.stringify(report([assertion({ ancestorTitles: null })])),
	"null duration": JSON.stringify(report([assertion({ duration: null, location: null, failureMessages: null })])),
	"null file start": JSON.stringify(report([assertion()], {}) && { ...report([assertion()]), testResults: [{ ...report([assertion()]).testResults[0], startTime: null }] }),
	"bad file status": JSON.stringify({ ...report([assertion()]), testResults: [{ ...report([assertion()]).testResults[0], status: "skipped" }] }),
	"no test results": JSON.stringify((({ testResults, ...rest }) => rest)(report([]))),
	"array meta": JSON.stringify(report([assertion({ meta: [1] })])),
	"invalid json": "{",
	"strict usage key": JSON.stringify(report([assertion({ meta: withRun({ usage: { ...run.usage, extra: 1 } }) })])),
	// Passthrough keys that differ from a read member only in case are not that member.
	"case-variant keys": JSON.stringify({ ...report([assertion({ FullName: "other", Status: "failed", Meta: {}, META: null })]), TestResults: [], testresults: [] }),
	"case-variant file key": JSON.stringify({ ...report([assertion()]), testResults: [{ ...report([assertion()]).testResults[0], AssertionResults: [] }] }),
};
const specialTask = { ...task, evalSet: "Ex<a>&\u2028\"q\"", caseId: "case \u00e9" };
const specialReports: Record<string, string> = {
	"special identity": JSON.stringify(report([assertion({ fullName: `${specialTask.evalSet} ${specialTask.caseId}` })])),
};
const observations = [];
const reportCases = [
	...Object.entries(reports).map(([name, content]) => ({ name, content, task })),
	...Object.entries(specialReports).map(([name, content]) => ({ name, content, task: specialTask })),
];
for (const { name, content, task } of reportCases) {
	const directory = await mkdtemp(join(tmpdir(), "evals-oracle-"));
	const reportPath = join(directory, "report.json");
	await writeFile(reportPath, content);
	const artifacts = join(directory, "artifacts");
	const observation = await readTaskObservation(task as never, reportPath, artifacts);
	const files: Record<string, string> = {};
	async function walk(path: string): Promise<void> {
		let entries;
		try {
			entries = await readdir(path, { withFileTypes: true });
		} catch {
			return;
		}
		for (const entry of entries) {
			const child = join(path, entry.name);
			if (entry.isDirectory()) await walk(child);
			else files[relative(artifacts, child)] = await readFile(child, "utf8");
		}
	}
	await walk(artifacts);
	observations.push({ name, task, report: content, observation, files });
	await rm(directory, { recursive: true, force: true });
}

process.stdout.write(`${JSON.stringify({ summaries, observations })}\n`);
