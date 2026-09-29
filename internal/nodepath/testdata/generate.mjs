// Generates resolve_cases.json from Node's path.win32.resolve and
// path.posix.resolve. Run with Node 24:
//   node internal/nodepath/testdata/generate.mjs > internal/nodepath/testdata/resolve_cases.json
// process.cwd and the "=C:" drive variables are replaced per case, so the
// expectations do not depend on the machine that generated them.
import path from "node:path";

if (!process.version.startsWith("v24.")) {
	throw new Error(`expected Node 24, got ${process.version}`);
}

const win = (cwd, args, env = {}) => ({ flavor: "win32", cwd, env, args });
const posix = (cwd, args) => ({ flavor: "posix", cwd, env: {}, args });
// A cwdThrows case makes process.cwd() throw, as libuv does for a deleted working directory; want is then the thrown error's code.
const winThrows = (args, env = {}) => ({ flavor: "win32", cwd: "", cwdThrows: true, env, args });
const posixThrows = (args) => ({ flavor: "posix", cwd: "", cwdThrows: true, env: {}, args });
const C = String.raw`C:\work\proj`;
const D = String.raw`D:\other`;
const UNC = String.raw`\\srv\share\dir`;

const cases = [
	// The lane's two bugs: rooted paths take the process drive; trailing dots survive.
	win(C, [String.raw`\tools\x.`]),
	win(C, [D, String.raw`\tools\x`]),
	win(C, [D, "/tools/x"]),
	win(C, [String.raw`D:\base`, String.raw`\x`]),
	win(D, [C, String.raw`\x`]),
	win(UNC, [String.raw`\x`]),
	win(UNC, [C, String.raw`\x`]),
	win(C, [String.raw`\tools\x.`, "y"]),
	win(C, [String.raw`x.`]),
	win(C, [String.raw`x..`]),
	win(C, [String.raw`x...\y`]),
	win(C, [String.raw`a\x.\..\y.`]),
	win(C, [String.raw`D:\tools\x.`]),
	win(C, [String.raw`D:\tools\x. `]),
	win(C, [String.raw`D:\tools\x .`]),
	win(C, ["."]),
	win(C, [""]),
	win(C, []),
	win(UNC, []),
	win(UNC, ["."]),
	win(UNC, [""]),
	win(C, ["..", "..", "..", ".."]),
	win(C, [String.raw`..\..\..\x`]),
	win(C, [String.raw`a\b\..\..\..\..\c`]),
	win(C, ["a/b/../c/"]),
	win(C, [String.raw`a\\b\\\c`]),
	win(C, ["a", "b", "c"]),
	win(C, ["a", "", "b"]),
	win(C, ["a", "/b", "c"]),
	win(C, ["a", String.raw`\b`, "c"]),
	win(C, ["/"]),
	win(C, ["\\"]),
	win(C, ["\\\\"]),
	win(C, ["\\\\\\"]),
	// Drive-relative paths.
	win(C, ["C:x"]),
	win(C, ["c:x"]),
	win(C, ["C:"]),
	win(C, ["D:x"]),
	win(C, ["D:"]),
	win(C, ["D:x"], { "=D:": String.raw`D:\dcwd` }),
	win(C, ["D:x"], { "=D:": String.raw`E:\wrong` }),
	win(C, ["d:x"], { "=D:": String.raw`D:\dcwd` }),
	win(C, ["D:x"], { "=D:": "D:" }),
	win(C, ["D:x"], { "=D:": String.raw`\\srv\share\dir` }),
	win(C, ["D:x"], { "=D:": "d:/slash" }),
	// charCodeAt(2) and toLowerCase work on UTF-16 units with full Unicode case mapping.
	win(C, ["K:a", "x"], { "=K:": String.raw`é\a\b` }),
	win(C, ["I:x"], { "=I:": String.raw`İ:\x` }),
	win(C, ["K:x"], { "=K:": String.raw`K:\k` }),
	win(String.raw`/é\x`, ["b:x"]),
	win(C, ["D:.."], { "=D:": String.raw`D:\a\b` }),
	win(C, ["D:..", "..", ".."], { "=D:": String.raw`D:\a\b` }),
	win(C, ["C:x", "D:y"]),
	win(C, ["D:y", "C:x"]),
	win(C, ["a", "D:y", "b"]),
	win(C, ["D:y", "b"]),
	win(C, [String.raw`D:\a`, "D:b", "C:c"]),
	win(C, [String.raw`D:\a`, "C:b"]),
	win(C, ["C:b", String.raw`D:\a`]),
	win(C, [String.raw`c:\A`, "C:b"]),
	win(String.raw`c:\lower`, ["C:b"]),
	win(String.raw`c:\lower`, ["x"]),
	win("C:", ["x"]),
	win("C:\\", ["x"]),
	win("C:\\", ["..", "x"]),
	// UNC and device roots.
	win(C, [String.raw`\\srv\share`]),
	win(C, ["\\\\srv\\share\\"]),
	win(C, [String.raw`\\srv\share\a\..\b`]),
	win(C, ["//srv/share/a/b"]),
	win(C, [String.raw`\\srv\share\..\..\x`]),
	win(C, [String.raw`\\srv`]),
	win(C, ["\\\\srv\\"]),
	win(C, [String.raw`\\srv\\share\x`]),
	win(C, [String.raw`\\.\PHYSICALDRIVE0`]),
	win(C, [String.raw`\\?\C:\a\..\b`]),
	win(C, [String.raw`\\?\UNC\srv\share\x`]),
	win(C, [String.raw`\\.\pipe\name`, "x"]),
	win(C, [String.raw`\\.`]),
	win(C, ["\\\\.\\"]),
	win(C, [String.raw`\\?\C:`, "x"]),
	win(C, [String.raw`\\srv\share`, String.raw`\\SRV\Share\x`]),
	win(C, [String.raw`\\srv\share\a`, String.raw`\\other\share\b`]),
	win(C, [String.raw`\\srv\share\a`, String.raw`\rooted`]),
	win(C, [String.raw`\\srv\share\a`, "D:x"]),
	// Long and unusual segments.
	win(C, [String.raw`a\...\b`]),
	win(C, [String.raw`a\.b\..c\d.`]),
	win(C, [String.raw`a\..\..\..`]),
	win(C, [String.raw`.\a\.\b\.`]),
	win(C, [String.raw`.hidden`]),
	win(C, ["a b", "c d"]),
	win(C, ["é\\ü.", "日本語\\x."]),
	win(C, ["😀\\x."]),
	win(C, ["a:b"]),
	win(C, ["1:x"]),
	win(C, ["::"]),
	win(C, [String.raw`C:\a:b`]),
	win(C, [String.raw`C:\\\a\\\b`]),
	win(C, [String.raw`C:/a/b/..//c/`]),
	win(C, [String.raw`C:\a\b\..\..\..\..\x`]),
	win(C, [String.raw`C:a\..\..\x`]),
	win(C, [String.raw`D:a\..\..\x`]),
	win(C, [String.raw`C:\tools\x.`]),
	win(C, [String.raw`c:\TOOLS\x.`]),
	win(C, ["~"]),
	win(C, ["~/x."]),
	// Pi's typical call shapes.
	win(C, [".pi", "skills"]),
	win(C, [String.raw`C:\Users\me`, "..", "session."]),
	win(C, [String.raw`C:\Users\me\.pi\agent\sessions\x.jsonl`, ".."]),
	win(C, [String.raw`C:\Users\me`, String.raw`\tools\x.`]),
	win(C, [String.raw`C:\Users\me`, String.raw`D:\tools\x.`]),
	// POSIX.
	posix("/work/proj", []),
	// Node 24 returns process.cwd() unnormalized for (), ("") and (".").
	posix("/work//proj/", []),
	posix("/work//proj/", [""]),
	posix("/work//proj/", ["."]),
	posix("/work//proj/", ["x"]),
	posix("/work//proj/", [".", ""]),
	posix("/work/proj", [""]),
	posix("/work/proj", ["."]),
	posix("/work/proj", ["x."]),
	posix("/work/proj", ["a", "b", "c"]),
	posix("/work/proj", ["/a", "b"]),
	posix("/work/proj", ["a", "/b", "c"]),
	posix("/work/proj", ["..", "..", "..", ".."]),
	posix("/work/proj", ["a/b/../../../../c"]),
	posix("/work/proj", ["a//b///c/"]),
	posix("/work/proj", ["/"]),
	posix("/work/proj", ["//"]),
	posix("/work/proj", ["//a//b"]),
	posix("/work/proj", ["/a/.../b/..x/y."]),
	posix("/work/proj", [String.raw`\a\b`]),
	posix("/work/proj", [String.raw`C:\a`, "b"]),
	posix("/", ["x"]),
	posix("/", ["..", "x"]),
	posix("/work/proj", ["é/ü.", "😀"]),
	// An unreadable working directory: Node throws only when it calls process.cwd().
	posixThrows(["x"]),
	posixThrows(["x", "y"]),
	posixThrows(["."]),
	posixThrows([""]),
	posixThrows([]),
	posixThrows(["", "x"]),
	posixThrows(["/abs"]),
	posixThrows(["x", "/abs", "y"]),
	posixThrows(["/", "x"]),
	winThrows(["x"]),
	winThrows(["."]),
	winThrows([""]),
	winThrows([]),
	winThrows([String.raw`\x`]),
	winThrows([String.raw`C:\x`]),
	winThrows(["C:x"]),
	winThrows(["C:x"], { "=C:": String.raw`C:\dcwd` }),
	winThrows(["x", String.raw`C:\y`]),
	winThrows([String.raw`\\srv\share\x`]),
	winThrows(["D:x"], { "=D:": "" }),
];

// Node drops "=C:" names from a real process.env on non-Windows hosts, so the
// tests swap in a plain object.
const realCwd = process.cwd;
const realEnv = process.env;
const resolved = cases.map((c) => {
	process.cwd = () => {
		if (c.cwdThrows) {
			throw Object.assign(new Error("ENOENT: process.cwd failed with error no such file or directory"), { code: "ENOENT" });
		}
		return c.cwd;
	};
	Object.defineProperty(process, "env", { value: { ...c.env }, configurable: true, writable: true });
	try {
		return { ...c, want: path[c.flavor].resolve(...c.args) };
	} catch (err) {
		if (!c.cwdThrows) throw err;
		return { ...c, want: "", throws: err.code };
	}
});
process.cwd = realCwd;
Object.defineProperty(process, "env", { value: realEnv, configurable: true, writable: true });

// path.win32.toNamespacedPath resolves its argument to classify it. The process cwd is C:\work\proj.
const namespacedInputs = [
	"", "C:", "C:\\", String.raw`C:\x`, String.raw`c:\x\..\y.`, "C:/x/y", "C:x", "x", "x.", ".", "..", "\\", "\\\\", String.raw`\x`, "/x", String.raw`\\srv\share`,
	String.raw`\\srv\share\dir\x`, "//srv/share/x", String.raw`\\?\C:\x`, String.raw`\\?\UNC\srv\share\x`, String.raw`\\.\pipe\name`, String.raw`\\.`, String.raw`\\?`,
	String.raw`D:\a\b`, "d:/a", "é", "日本語\\x", String.raw`\\srv\share\..\..\x`, String.raw`\\srv`,
	// A device or long path is returned resolved, not as given (Node 24 lib/path.js win32.toNamespacedPath ends with `return resolvedPath`).
	"\\\\?\\foo/bar", String.raw`\\.\pipe/x/../y`, String.raw`\\?\C:\a\..\b`, "//./x", String.raw`\\.\pipe\a/b`,
];
// With an empty process.cwd() a relative path can resolve to at most two UTF-16 code units, and toNamespacedPath then returns its argument.
const shortInputs = ["./日", "a/../日", "./ab", String.raw`.\ab`, "./abc", "./\u{1F600}", "./\u{1F600}x"];
const namespacedFor = (cwd, inputs) => {
	process.cwd = () => cwd;
	Object.defineProperty(process, "env", { value: {}, configurable: true, writable: true });
	return inputs.map((input) => ({ cwd, input, want: path.win32.toNamespacedPath(input) }));
};
const namespaced = [...namespacedFor(C, namespacedInputs), ...namespacedFor("", shortInputs)];
process.cwd = realCwd;
Object.defineProperty(process, "env", { value: realEnv, configurable: true, writable: true });

const absoluteInputs = [
	"", ".", "a", "/", "\\", "//", "\\\\", "/a", "\\a", "\\\\a\\b", String.raw`\\srv\share`, "C:", "C:\\", "C:/", "C:\\x", "c:/x", "C:x", "1:\\x", "::\\", String.raw`\\.\pipe`, "é:\\", "~", "a\\b", "..", "C", "C:a\\b", "z:/", "Z:\\\\",
];
const isAbsolute = ["win32", "posix"].flatMap((flavor) =>
	absoluteInputs.map((input) => ({ flavor, input, want: path[flavor].isAbsolute(input) })),
);

console.log(JSON.stringify({ resolve: resolved, isAbsolute, namespaced }, null, "\t"));
