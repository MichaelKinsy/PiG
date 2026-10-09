// Constructs every packages/server/src/errors.ts class from the pinned upstream mirror and prints name, code, message and the instanceof ServerError / Error answers.
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const m = await import(pathToFileURL(join(process.argv[2], ".upstream", "current", "packages", "server", "src", "errors.ts")).href);
const out = {};
for (const [k, mk] of Object.entries({ ServerError: () => new m.ServerError("service_not_found", "gone"), WrongServerError: () => new m.WrongServerError(), SessionNotFoundError: () => new m.SessionNotFoundError(), SessionNotFoundErrorMsg: () => new m.SessionNotFoundError("custom"), SessionAmbiguousError: () => new m.SessionAmbiguousError(), SessionNotAttachedError: () => new m.SessionNotAttachedError(), ServerDrainingError: () => new m.ServerDrainingError() })) { const e = mk(); out[k] = { name: e.name, code: e.code, message: e.message, isServerError: e instanceof m.ServerError, isError: e instanceof Error }; }
console.log(JSON.stringify(out));
