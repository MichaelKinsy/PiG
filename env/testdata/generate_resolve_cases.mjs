// Generates resolve_cases.json from Pi's RemoteExecutionEnv path resolution (packages/env/src/remote-env.ts #resolve,
// reached through absolutePath) with a connection that reports a fixed remote system. Node 24 runs Pi's TypeScript
// directly; a resolve hook maps @earendil-works/pi-durable/env to its source. Run from the repository root:
//   node env/testdata/generate_resolve_cases.mjs > env/testdata/resolve_cases.json
// The source tree is .upstream/current unless PI_UPSTREAM_ROOT names another.
import { register } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";

if (!process.version.startsWith("v24.")) {
	throw new Error(`expected Node 24, got ${process.version}`);
}
const root = path.resolve(process.env.PI_UPSTREAM_ROOT ?? ".upstream/current");
const durableEnv = pathToFileURL(path.join(root, "packages/durable/src/env/index.ts")).href;
const hook = `export async function resolve(specifier, context, next) {
	if (specifier === "@earendil-works/pi-durable/env") return { url: ${JSON.stringify(durableEnv)}, shortCircuit: true };
	return next(specifier, context);
}`;
register(`data:text/javascript,${encodeURIComponent(hook)}`);
const { RemoteExecutionEnv } = await import(pathToFileURL(path.join(root, "packages/env/src/remote-env.ts")).href);

const systems = [
	{
		info: { os: "linux", home: "/home/me", cwd: "/srv/daemon", driveCwds: {} },
		cwds: ["/work", "/"],
		paths: [
			"", ".", "~", "~/", "~/a/b", "~\\a", "~x", "a/b", "../x", "a//b/", "/", "/abs/../y", "C:\\x", "file:///tmp/a%20b",
			"file:///tmp/%E2%82%AC", "file://localhost/etc/hosts", "file://host/x", "file:///a%2Fb", "file:relative", "~/../..",
		],
	},
	{
		info: {
			os: "windows",
			home: "C:\\Users\\me",
			cwd: "C:\\daemon",
			driveCwds: { "E:": "E:\\work", "F:": "C:\\odd" },
		},
		cwds: ["C:\\work", "D:\\proj", "c:\\lower"],
		paths: [
			"", ".", "~", "~/a", "~\\a", "~x", "rel\\x", "a/b", "..\\..\\..", "C:\\x\\..\\y", "C:/x/y", "c:rel", "C:rel", "D:rel",
			"d:", "D:", "D:\\abs", "E:rel", "e:rel", "F:rel", "Z:rel", "Z:", "\\\\server\\share\\x", "//server/share",
			"file:///C:/a%20b", "file://server/share/x", "file:///C:/a%5Cb", "file:///C:/a%2Fb", "file:///tmp/x", "C:",
		],
	},
];
const cases = [];
for (const { info, cwds, paths } of systems) {
	for (const cwd of cwds) {
		const env = new RemoteExecutionEnv({ connection: { info: async () => info }, id: "pi-env:test", cwd });
		for (const input of paths) {
			const result = await env.absolutePath(input, {});
			cases.push({ info, cwd, path: input, ...(result.ok ? { resolved: result.value } : { error: result.error.message }) });
		}
	}
}
process.stdout.write(`${JSON.stringify({ cases })}\n`);
