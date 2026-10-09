// Generates join_cases.json from Node's path.win32 and path.posix join, normalize and basename. Run with Node 24:
//   node internal/nodepath/testdata/generate_join.mjs > internal/nodepath/testdata/join_cases.json
import path from "node:path";

if (!process.version.startsWith("v24.")) {
	throw new Error(`expected Node 24, got ${process.version}`);
}

const pieces = [
	"", ".", "..", "/", "\\", "//", "\\\\", "///", "a", "b", "a/b", "a\\b", "/a", "\\a", "a/", "a\\", "../a", "..\\a",
	"a/../b", "a\\..\\b", "./a", ".\\a", "C:", "C:\\", "C:/", "C:a", "c:\\x", "D:..", "C:\\a\\..\\..", "//server/share",
	"\\\\server\\share", "\\\\server\\share\\dir", "//server/share/", "//server", "\\\\server\\", "\\\\?\\C:\\x", "\\\\.\\pipe\\x",
	"a:b", "a:", "x:y:z", "a:\\b", "ab:c", ":", ":a", "a\\:b", "...", "a...", "é", "é\\ü", "日本/語", "a b", " ", "a//b", "a\\\\b",
	"foo/bar.txt", "C:\\Users\\me\\.pi\\mobile\\tools", "/home/me/.pi", "/tmp/", "tmp-",
];

const joins = [];
const flavors = { posix: path.posix, win32: path.win32 };
for (const [flavor, impl] of Object.entries(flavors)) {
	for (const a of pieces) {
		joins.push({ flavor, args: [a], want: impl.join(a), normalize: impl.normalize(a), basename: impl.basename(a) });
		for (const b of pieces) {
			joins.push({ flavor, args: [a, b], want: impl.join(a, b) });
		}
	}
	for (const args of [[], ["a", "b", "c"], ["", "", ""], ["/", "a", "..", "b"], ["C:\\", "..", "x"], ["//server", "share", "x"], ["\\\\", "a"], ["a", "", "b"], ["x", "..", "..", "y"], ["~", "a"]]) {
		joins.push({ flavor, args, want: impl.join(...args) });
	}
}
process.stdout.write(`${JSON.stringify({ node: process.version, cases: joins })}\n`);
