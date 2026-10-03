// Generates regexp-oracle.json: JavaScript RegExp (m flag, optionally i) results for the constructs translateJSRegExp rewrites.
// Usage: node regexp-oracle.mjs > regexp-oracle.json
const cases = [
	["\\bkey\\b", "i", "\u212Aey key"],
	["k", "i", "\u212A K k"],
	["[a-z]+", "i", "\u212A\u017Fs"],
	["\\b\\d+", "", "Параметр1"],
	["\\B\\d", "", "Параметр1 x1"],
	["a.b", "", "a\u2028b a\rb axb"],
	["x$", "", "x\u2029y"],
	["^y", "", "x\u2028y"],
	["[^]", "", "\n"],
	["a[]|b", "", "ab"],
	["\\cJ", "", "a\nb"],
	["\\c1", "", "\\c1"],
	["[\\c1]", "", "\u0011"],
	["\\101\\0", "", "A\u0000"],
	["\\8", "", "8"],
	["\\1010", "", "A0"],
	["\\477", "", "'7"],
	["\\400", "", " 0"],
	["\\08", "", "\u00008"],
	["\\377", "", "\u00ff"],
	["[\\4-\\60]+", "", "\u0004\u00100"],
	["x{2", "", "x{2"],
	["x{2}", "", "xxx"],
	["\\u{2}", "", "uu"],
	["(?<q>['\"]).*?\\k<q>", "", "say 'hi' \"yo\""],
	["(?<q>a)(b)\\2", "", "abb"],
	["\\k<q>", "", "k<q>"],
	["(a)|\\1b", "", "b"],
	["[\\d-z]+", "", "1-z"],
	["\\p{L}", "", "p{L}"],
	["[\\uD800-\\uDBFF]", "", "a\uD83D\uDE00"],
	[".", "", "\uD83D\uDE00"],
	["\\s+", "", "a\u00a0\ufeff\u0085b"],
	["[^\\s]+", "", "\u0085x"],
	["\\w+", "i", "\u017Fx"],
	["[^a]", "i", "A"],
	["\u00e9", "i", "\u00c9"],
	["\u03c3", "i", "\u03c2\u03a3"],
	["(?<=\\$)\\w+", "", "$var"],
	["\\]\\}", "", "]}"],
	["]}", "", "]}"],
	["[\\b]", "", "a\bb"],
	["\\x4", "", "x4"],
	["\\u004", "", "u004"],
];
const units = (text) => Array.from({ length: text.length }, (_, i) => text.charCodeAt(i));
const results = cases.map(([source, flags, input]) => {
	const match = new RegExp(source, `m${flags}`).exec(input);
	return { source, ignoreCase: flags === "i", input: units(input), match: match ? { index: match.index, length: match[0].length } : null };
});
console.log(JSON.stringify(results, null, "\t"));
