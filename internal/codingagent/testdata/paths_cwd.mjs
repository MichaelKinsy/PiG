// Drive the installed Pi's isLocalPath, getCwdRelativePath, formatPathRelativeToCwdOrAbsolute and canonicalizePath, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const p = await import(pathToFileURL(root + "dist/utils/paths.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const attempt = (fn) => { try { const v = fn(); return { ok: v === undefined ? null : v }; } catch (error) { return { err: String(error && error.message || error) }; } };
process.stdout.write(JSON.stringify(JSON.parse(input).map(([value, cwd]) => ({
	local: p.isLocalPath(value),
	rel: attempt(() => p.getCwdRelativePath(value, cwd)),
	fmt: attempt(() => p.formatPathRelativeToCwdOrAbsolute(value, cwd)),
	canon: p.canonicalizePath(value),
}))));
