// Generates errors-golden.json: Pi's packages/client/src/errors.ts error classes and toDisconnectedError run on a fixed corpus.
// Run from the repository root with Node >= 24 (native type stripping):
//   node internal/experimental/client/testdata/errors-oracle.mjs > internal/experimental/client/testdata/errors-golden.json
// The upstream mirror is .upstream/current (override with PI_UPSTREAM).
import { writeSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_UPSTREAM ?? ".upstream/current";
const { ServerError, DisconnectedError, ClientDisposedError, toDisconnectedError } = await import(
	pathToFileURL(resolve(root, "packages/client/src/errors.ts")).href
);

const describe = (error) => ({
	name: error.name,
	message: error.message,
	text: String(error),
	isError: error instanceof Error,
	hasCause: "cause" in error,
	causeMessage: error.cause instanceof Error ? error.cause.message : null,
	code: error.code ?? null,
});

const cases = { server: [], disconnected: [], disposed: describe(new ClientDisposedError()), to: [] };
for (const [code, message] of [["invalid_request", "bad call"], ["not_found", ""], ["internal_error", "multi\nline \u{1F600}"]]) {
	cases.server.push({ inCode: code, inMessage: message, ...describe(new ServerError({ code, message })) });
}
for (const args of [[], [undefined, new Error("ignored")], ["lost"], ["lost", new Error("socket reset")], ["", undefined], ["", new Error("")]]) {
	cases.disconnected.push({ inMessage: args[0] ?? null, inCause: args[1] instanceof Error ? args[1].message : null, ...describe(new DisconnectedError(...args)) });
}
for (const input of ["plain failure", "", "multi\nline"]) {
	const result = toDisconnectedError(new Error(input));
	cases.to.push({ input, passthrough: false, ...describe(result) });
}
{
	const original = new DisconnectedError("already disconnected", new Error("inner"));
	const result = toDisconnectedError(original);
	cases.to.push({ input: "already disconnected", passthrough: result === original, ...describe(result) });
}
writeSync(1, `${JSON.stringify(cases, null, "\t")}\n`);
