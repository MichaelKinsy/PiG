// Generates relative_cases.json from Node's path.win32.relative and
// path.posix.relative. Run with Node 24:
//   node internal/nodepath/testdata/generate_relative.mjs > internal/nodepath/testdata/relative_cases.json
// process.cwd is replaced per flavor, so the expectations do not depend on the machine that generated them.
import path from "node:path";

if (!process.version.startsWith("v24.")) {
	throw new Error(`expected Node 24, got ${process.version}`);
}
const posixPool = [
	"/", "//", "/a", "/a/", "/a/b", "/a/b/", "/a/b/c", "/a/bc", "/a/b/../c", "/ab", "/a/b/c/d", "/home/user", "/home/user/", "/home/user/file.txt",
	"/home/user/project/", "/tmp/results/file.txt", "/tmp/results/dir/", "/workspace/project", "/home/user/file\\", "/é/ü", "/é/ä", "/😀/x", "/😁/x",
	"", ".", "..", "a", "a/b", "../x", "x/../y", "/a/./b", "/A/b",
];
// "|" stands for a backslash, so a pool entry can end in a separator.
const winPool = [
	"I:|", "I:|AI", "I:|AI|Models", "I:|AI|Models|", "I:|AI|Models2|file.txt", "I:|AI|Models|TextGen|gemma4|", "I:/AI/Models/TextGen/gemma4/", "i:|ai|models", "I:|AI|models|x",
	"C:|work|proj", "C:|work|proj|a", "C:|work|project", "D:|other", "D:|", "C:|", "C:", "||srv|share|dir", "||srv|share|dir|x", "||SRV|Share|dir|x", "||other|share|a", "||srv|share",
	"||?|C:|a|b", "||.|pipe|x", "AI|Models|TextGen|gemma4|", "", ".", "..", "a", "..|x", "C:|é|ü", "C:|É|ü", "C:|İ|x", "C:|i̇|x", "C:|Σ|x", "C:|ς|x", "C:|😀|x", "|rooted|x", "C:|a||b", "C:|a|b|", "c:|A|B", "C:|work", "C:|w", "C:|x|y", "C:|work|proj|a|b", "||srv", "||srv|share|", "C:|work|", "D:|x", "D:|xy", "C:|ab", "C:|a", "||sr|share|x", "||srv|share|x", "||srv|s|x", "||sr|x", "||srv|x", "||sv|share|x",
	// İ lowercases to two UTF-16 units, so these take win32.relative's segment-comparison branch.
	"C:|İx|a", "C:|İx|a|B|c", "C:|İ|a|b", "C:|İ|c", "C:|x|İ|y", "c:|i̇x|A", "C:|İx|", "||srv|İ|x",
].map((p) => p.replaceAll("|", "\\"));
const cwds = { posix: "/work/proj", win32: "C:\\work\\proj" };
const cases = [];
const realCwd = process.cwd;
for (const [flavor, pool] of [["posix", posixPool], ["win32", winPool]]) {
	process.cwd = () => cwds[flavor];
	for (const from of pool) for (const to of pool) cases.push({ flavor, cwd: cwds[flavor], from, to, want: path[flavor].relative(from, to) });
}
process.cwd = realCwd;
process.stdout.write(JSON.stringify({ relative: cases }, null, 1) + "\n");
