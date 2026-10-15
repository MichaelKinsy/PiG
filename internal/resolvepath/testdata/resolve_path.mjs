// Drive the installed Pi's normalizePath, resolvePath and normalizeWindowsShellPath, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { normalizePath, resolvePath, normalizeWindowsShellPath } = await import(pathToFileURL(root + "dist/utils/paths.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const attempt = (fn) => { try { return { ok: fn() }; } catch (error) { return { err: String(error && error.message || error) }; } };
process.stdout.write(JSON.stringify(JSON.parse(input).map(([input, base, trim]) => ({
	normalize: attempt(() => normalizePath(input)),
	resolve: attempt(() => resolvePath(input, base === null ? undefined : base, trim ? { trim: true } : {})),
	shell: normalizeWindowsShellPath(input),
}))));
