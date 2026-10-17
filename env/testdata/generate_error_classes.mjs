// Generates error_classes_cases.json from Pi's RemoteError (packages/env/src/connection.ts) and SshError (packages/env/src/ssh.ts),
// both of which import only Node's built-ins, so Node 24 runs them directly. Run from the repository root:
//   node env/testdata/generate_error_classes.mjs > env/testdata/error_classes_cases.json
// The source tree is .upstream/current unless PI_UPSTREAM_ROOT names another.
import { pathToFileURL } from "node:url";
import path from "node:path";

const root = process.env.PI_UPSTREAM_ROOT ?? ".upstream/current";
const load = (file) => import(pathToFileURL(path.resolve(root, "packages/env/src", file)).href);
const { RemoteError } = await load("connection.ts");
const { SshError } = await load("ssh.ts");

const remoteFields = [
	{}, { message: "boom" }, { message: "" }, { message: 5 }, { code: "ENOENT", message: "no such file", path: "/a/b" },
	{ code: 7, path: 3 }, { code: "", path: "" }, { code: "EACCES", message: "denied", extra: { n: [1, 2] }, path: "/x" }, { message: "multi\nline \u{1F600}" },
];
const describe = (error) => ({ name: error.name, message: error.message, text: String(error), isError: error instanceof Error, hasCause: "cause" in error });
const cases = { remote: [], ssh: [] };
for (const fields of remoteFields) {
	const error = new RemoteError(fields);
	cases.remote.push({ fields, ...describe(error), code: error.code, path: error.path ?? null, outFields: error.fields });
}
for (const [message, exitCode, stderr] of [["ssh failed", 255, "Permission denied"], ["", null, ""], ["killed", null, "line1\nline2"], ["x", 0, "\u{1F600}"]]) {
	const error = new SshError(message, exitCode, stderr);
	cases.ssh.push({ inMessage: message, inExitCode: exitCode, inStderr: stderr, ...describe(error), exitCode: error.exitCode, stderr: error.stderr });
}
process.stdout.write(`${JSON.stringify(cases, null, "\t")}\n`);
