// Regenerates pi_errors.json: Pi's packages/chord/src/errors.ts (ChordError, isChordError, forbidden, invalid, asChordError, runAll)
// run over the inputs below. Run from the repository root with Node 24 or later:
//
//	node chord/host/testdata/pi_errors.mjs > chord/host/testdata/pi_errors.json
//
// Each case holds its inputs and what Pi's code returned for them; the Go test replays the inputs through chord/host.
import {
	asChordError,
	CHORD_ERROR_CODES,
	ChordError,
	forbidden,
	invalid,
	isChordError,
	runAll,
} from "../../../.upstream/current/packages/chord/src/errors.ts";

const view = (error) => ({
	name: error.name,
	message: error.message,
	code: error.code,
	reason: error.reason === undefined ? null : error.reason,
	retryable: error.retryable,
	details: error.details === undefined ? null : error.details,
	cause: error.cause instanceof Error ? error.cause.message : null,
});

const construct = [];
for (const code of CHORD_ERROR_CODES) {
	for (const reason of [undefined, "", "args", "not_host_lifetime"]) {
		const options = reason === undefined ? undefined : { reason };
		construct.push({ code, message: "m: with colon", reason: reason === undefined ? null : reason, expect: view(new ChordError(code, "m: with colon", options)) });
	}
}
construct.push({
	code: "invalid",
	message: "bad",
	reason: "state",
	details: [{ instancePath: "/a", message: "x" }],
	cause: "disk",
	expect: view(new ChordError("invalid", "bad", { reason: "state", details: [{ instancePath: "/a", message: "x" }], cause: new Error("disk") })),
});
construct.push({ code: "gone", message: "", reason: null, expect: view(new ChordError("gone", "")) });

const is = [];
for (const code of CHORD_ERROR_CODES) {
	const error = new ChordError(code, "x");
	is.push({ kind: "chord", code, checks: [null, ...CHORD_ERROR_CODES].map((check) => ({ check, result: isChordError(error, check === null ? undefined : check) })) });
}
is.push({ kind: "plain", checks: [null, "gone"].map((check) => ({ check, result: isChordError(new Error("plain"), check === null ? undefined : check) })) });
is.push({ kind: "aggregate", checks: [null, "gone"].map((check) => ({ check, result: isChordError(new AggregateError([new ChordError("gone", "g"), new ChordError("gone", "g")], "two"), check === null ? undefined : check) })) });

const helpers = [
	{ fn: "forbidden", args: [], expect: view(forbidden()) },
	{ fn: "forbidden", args: [""], expect: view(forbidden("")) },
	{ fn: "forbidden", args: ["not for you"], expect: view(forbidden("not for you")) },
	{ fn: "invalid", args: ["cycle", "loop", null], expect: view(invalid("cycle", "loop", undefined)) },
	{ fn: "invalid", args: ["args", "bad: args", 3], expect: view(invalid("args", "bad: args", 3)) },
];

const own = new ChordError("gone", "g", { reason: "r" });
const as = [
	{ kind: "chord", context: "ctx", message: "g", same: asChordError(own, "ctx") === own },
	{ kind: "plain", context: "loading x", message: "boom", expect: view(asChordError(new Error("boom"), "loading x")) },
	{ kind: "plain", context: "", message: "", expect: view(asChordError(new Error(""), "")) },
	{ kind: "wrapped", context: "c", message: "outer", expect: view(asChordError(new Error("outer", { cause: own }), "c")) },
];

const runs = [];
const programs = [
	[],
	[{ ok: true }],
	[{ throw: "a" }],
	[{ ok: true }, { throw: "a" }, { ok: true }],
	[{ throw: "a" }, { throw: "b" }],
	[{ throw: "a" }, { ok: true }, { throw: "b" }, { throw: "c" }],
];
for (const program of programs) {
	const log = [];
	const run = program.map((item, index) => () => { log.push(index); if (item.throw !== undefined) throw new Error(item.throw); });
	let outcome;
	try {
		await runAll(run, "several failed");
		outcome = { result: "ok" };
	} catch (error) {
		outcome = error instanceof AggregateError
			? { result: "aggregate", message: error.message, errors: error.errors.map((e) => e.message) }
			: { result: "error", message: error.message };
	}
	runs.push({ steps: program, ran: log, outcome });
}

process.stdout.write(`${JSON.stringify({ codes: [...CHORD_ERROR_CODES], construct, is, helpers, as, runs }, null, "\t")}\n`);
