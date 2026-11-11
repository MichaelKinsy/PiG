// Generates to_file_error_cases.json from Pi's errors.ts (packages/env/src/errors.ts): toFileError and abortResult on a fixed corpus.
// errors.ts imports FileError and err from "@earendil-works/pi-durable/env" and RemoteError from ./connection.ts. Node cannot resolve the
// bare specifier without the built package, so a resolve hook maps it to the pinned source file packages/durable/src/env/index.ts (which
// imports only a type, so Node 24 strips it). Run from the repository root:
//   node env/testdata/generate_to_file_error.mjs > env/testdata/to_file_error_cases.json
// The source tree is .upstream/current unless PI_UPSTREAM_ROOT names another.
import { realpathSync } from "node:fs";
import { registerHooks } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";

// The mirror root is a symlink; resolving it keeps the module URLs the hook returns and the ones the loader derives identical.
const root = realpathSync(process.env.PI_UPSTREAM_ROOT ?? ".upstream/current");
const durableEnv = pathToFileURL(path.resolve(root, "packages/durable/src/env/index.ts")).href;
registerHooks({
	resolve(specifier, context, nextResolve) {
		if (specifier === "@earendil-works/pi-durable/env") return { url: durableEnv, shortCircuit: true };
		return nextResolve(specifier, context);
	},
});
const load = (file) => import(pathToFileURL(path.resolve(root, file)).href);
const { abortResult, toFileError } = await load("packages/env/src/errors.ts");
const { RemoteError } = await load("packages/env/src/connection.ts");
const { FileError } = await load("packages/durable/src/env/index.ts");

const described = (error) => ({ code: error.code, message: error.message, path: error.path ?? null, hasCause: error.cause !== undefined });
const fallbacks = [undefined, "/fallback", ""];
const codes = ["aborted", "ENOENT", "EACCES", "EPERM", "ENOTDIR", "EISDIR", "EINVAL", "SYMLINK", "NOT_REGULAR", "EEXIST", "ENOSPC", "unknown", "", "enoent"];
const remoteFields = [];
for (const code of codes) {
	remoteFields.push({ code, message: `m-${code}`, path: "/own" }, { code, message: `m-${code}` }, { code, message: `m-${code}`, path: "" });
}
remoteFields.push({}, { message: "boom" }, { code: 7, path: 3, message: 9 }, { code: "ENOENT", path: 3 }, { code: "ENOENT", message: "multi\nline \u{1F600}", path: "/x" });

const toFile = [];
for (const fields of remoteFields) {
	for (const fallback of fallbacks) {
		const input = new RemoteError(fields);
		const result = toFileError(input, fallback);
		toFile.push({ kind: "remote", fields, fallback: fallback ?? null, result: described(result), causeIsInput: result.cause === input, isFileError: result instanceof FileError });
	}
}
for (const message of ["boom", "", "multi\nline"]) {
	for (const fallback of fallbacks) {
		const input = new Error(message);
		const result = toFileError(input, fallback);
		toFile.push({ kind: "error", message, fallback: fallback ?? null, result: described(result), causeIsInput: result.cause === input, isFileError: result instanceof FileError });
	}
}
for (const [code, message, filePath] of [["invalid", "kept", "/p"], ["not_found", "gone", undefined], ["aborted", "", ""]]) {
	for (const fallback of fallbacks) {
		const input = new FileError(code, message, filePath);
		const result = toFileError(input, fallback);
		toFile.push({ kind: "file", fileCode: code, message, path: filePath ?? null, fallback: fallback ?? null, result: described(result), same: result === input });
	}
}

const abort = [];
for (const aborted of [false, true, null]) {
	for (const filePath of [undefined, "/p", ""]) {
		const signal = aborted === null ? undefined : AbortSignal.abort();
		const live = aborted === false ? new AbortController().signal : signal;
		const result = abortResult(live, filePath);
		abort.push({ signal: aborted === null ? "absent" : aborted ? "aborted" : "live", path: filePath ?? null, result: result === undefined ? null : { ok: result.ok, error: described(result.error), isFileError: result.error instanceof FileError } });
	}
}
process.stdout.write(`${JSON.stringify({ toFile, abort }, null, "\t")}\n`);
