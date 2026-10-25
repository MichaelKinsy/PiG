// Run Pi's error classes (packages/durable/src/errors.ts, imported as-is with types stripped) over ops from stdin.
// Op: {class: "StorageRequestError", message} | {class: "SessionFailed", cause?: {name, message} | string | null | "undefined"} | {class: "ConversationBusy", id} | {class: "ReadAfterWrite", method} | {class: "ReadOnlySession", what} | {op: "errorMessage", value}
// stdout: per op {name, message, instanceofError, hasCause, cause} for a class, {message} for errorMessage.
// usage: node errors_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const errors = await import(pathToFileURL(`${packages}durable/src/errors.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const describe = (error) => ({
	name: error.name,
	message: error.message,
	instanceofError: error instanceof Error,
	hasCause: "cause" in error,
	cause: error.cause === undefined ? null : error.cause instanceof Error ? { name: error.cause.name, message: error.cause.message } : error.cause,
});
const out = [];
for (const op of JSON.parse(input)) {
	switch (op.class ?? op.op) {
		case "StorageRequestError":
			out.push(describe(new errors.StorageRequestError(op.message)));
			break;
		case "SessionFailed": {
			const cause = op.cause === "undefined" ? undefined : op.cause !== null && typeof op.cause === "object" ? Object.assign(new Error(op.cause.message), { name: op.cause.name }) : op.cause;
			out.push(describe(new errors.SessionFailed(cause)));
			break;
		}
		case "ConversationBusy":
			out.push(describe(new errors.ConversationBusy(op.id)));
			break;
		case "ReadAfterWrite":
			out.push(describe(new errors.ReadAfterWrite(op.method)));
			break;
		case "ReadOnlySession":
			out.push(describe(new errors.ReadOnlySession(op.what)));
			break;
		case "errorMessage":
			out.push({ message: errors.errorMessage(op.value) });
			break;
	}
}
process.stdout.write(JSON.stringify(out));
