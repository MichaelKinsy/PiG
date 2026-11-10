// Records what Pi's NodeIncoming.of (packages/chord/src/node/http/node.ts:103-117) makes of a request's Host and target:
// `new URL("http://" + host + target)`, a 400 when it throws, a 403 when the URL has userinfo, else its href. Each line of
// the output is [host, target, {"href": ...} | {"status": ...}]. Hosts and targets are the Latin-1 strings Node's HTTP parser
// hands out: only printable ASCII (0x21 to 0x7e) reaches a handler in a target (llhttp answers any other byte 400), and a Host
// value is any byte but a control character.
//
//   node url_probe.cjs > url_corpus.jsonl      (the pinned Node: .node-version)
"use strict";

const out = [];
function record(host, target) {
	let result;
	try {
		const url = new URL(`http://${host}${target}`);
		result = url.username !== "" || url.password !== "" ? { status: 403 } : { href: url.href };
	} catch {
		result = { status: 400 };
	}
	out.push(JSON.stringify([host, target, result]));
}

const printable = [];
for (let code = 0x21; code <= 0x7e; code++) printable.push(String.fromCharCode(code));

// Targets over the default Host.
const targets = ["/", "//", "///", "//[", "//a", "/a", "/a/", "/a/b", "/a/b/", "/a//b", "/a/./b", "/a/.", "/a/..", "/a/../b", "/a/../../b", "/../a", "/./a", "/.", "/..", "/a/b/../..", "/a/b/../../..", "/a/%2e/b", "/a/%2E/b", "/a/%2e%2e/b", "/a/%2E%2E/b", "/a/.%2e/b", "/a/%2e./b", "/a/..%2f/b", "/a/%2e%2e", "/a/%2e", "/a%2fb", "/a%2Fb", "/a\\b", "/a\\..\\b", "/a\\.\\b", "\\", "/\\", "/\\\\", "/a/..\\b", "/a/.%5c/b", "/a?b", "/a?", "/a?b=c&d=e", "/a?b#c", "/a#c", "/a?#", "/?", "/?x", "/#", "/a/../?x", "/a/./?x", "/%", "/%z", "/%zz", "/%4", "/%41", "/%7e", "/%00", "/%ff", "/a%", "/a?%", "/a?%zz", "/a;b", "/a:b", "/c:/d", "/a@b", "/@", "/a=b", "/a,b", "/a$b", "/a&b", "/a+b", "/a!b", "/a*b", "/a(b)", "/a'b", '/a"b', "/a<b>", "/a`b", "/a{b}", "/a|b", "/a^b", "/a[b]", "/a]b", "/a?'", "/a?\"", "/a?`", "/a?{}", "/a?^", "/a?|", "/a?\\", "/a?[]", "/a?<>", "/a?a=b%20c", "/a/b?c/../d", "/a/b?../", "/a?x=/..", "/" + "a".repeat(2000), "/?" + "a".repeat(2000), "*"];
for (const target of targets) record("localhost", target);
for (const c of printable) {
	record("localhost", `/a${c}b`);
	record("localhost", `/${c}`);
	record("localhost", `/a?b${c}c`);
	record("localhost", `/a/${c}/../b`);
}
// Every two-character percent-encoded dot form.
for (const a of [".", "%2e", "%2E"]) for (const b of [".", "%2e", "%2E", ""]) for (const sep of ["/", "\\"]) record("localhost", `/x${sep}${a}${b}${sep}y`);

// Hosts over a plain target.
const hosts = ["localhost", "LOCALHOST", "LocalHost", "localhost:80", "localhost:8080", "localhost:08080", "localhost:0", "localhost:00", "localhost:65535", "localhost:65536", "localhost:99999999999999999999", "localhost:", "localhost:80:80", "localhost:a", "localhost:8a", "localhost:-1", "localhost:+1", "localhost: 80", "localhost:80 ", " localhost", "localhost ", "127.0.0.1", "127.0.0.1:80", "127.1", "0x7f.1", "0x7f.0.0.1", "0177.0.0.1", "2130706433", "1.2.3", "1.2.3.4.5", "256.1.1.1", "1.2.3.256", "0.0.0.0", "01.2.3.4", "[::1]", "[::1]:80", "[::1]:8080", "[::FFFF:7F00:1]", "[::ffff:127.0.0.1]", "[1::2::3]", "[::1", "::1]", "[::1]x", "[::1]:", "[::1]:a", "[]", "[:::]", "[1:2:3:4:5:6:7:8]", "[1:2:3:4:5:6:7:8:9]", "[0:0:0:0:0:0:0:1]", "[v1.a]", "example.com", "example.com.", "EXAMPLE.COM", "a.b.c.d", "a..b", ".a", "a.", "-a", "a-", "a_b", "xn--e1afmkfd", "XN--E1AFMKFD", "\xe4", "\xc3\xa4", "ex\xe4mple.com", "a\xffb", "a%20b", "%41", "%6cocalhost", "%6Cocalhost", "local%68ost", "%", "%z", "a%", "%00", "%2e", "%2e%2e", "a/b", "a/", "/", "a?b", "a#b", "a\\b", "a@b", "@", "a@", "@a", "a:b@c", "a:b@c:80", "user:pass@localhost", "user@localhost", ":@localhost", "a@b@c", "a b", "a\tb", "a^b", "a|b", "a<b", "a>b", "a[b", "a]b", "a{b}", "a`b", "a'b", 'a"b', "a;b", "a,b", "a=b", "a&b", "a+b", "a$b", "a!b", "a*b", "a(b)", "a~b", "a:b", "a:", ":a", ":", "::", ":80", "", "invalid", "0", "a".repeat(300), "a.".repeat(100) + "a", "\u00ff"];
for (const host of hosts) record(host, "/p");
// IPv6 compression: the first longest run of two or more zero pieces becomes `::`, a lone zero piece stays.
for (const host of ["[1:0:2:3:4:5:6:7]", "[1:0:0:2:0:0:3:4]", "[1:0:0:2:0:0:0:3]", "[0:0:1:0:0:0:0:2]", "[1:0:0:0:2:0:0:3]", "[0:1:0:2:0:3:0:4]", "[0:0:0:0:0:0:0:0]", "[0:0:0:0:0:0:0:1]", "[1:0:0:0:0:0:0:0]", "[1:2:3:4:5:6:0:0]", "[0:0:3:4:5:6:7:8]", "[1:0:3:0:0:6:7:8]", "[1::3:0:0:6:7:8]", "[1:2:3:4:5:6:7::]"]) record(host, "/p");
for (const c of printable) {
	record(`a${c}b`, "/p");
	record(`a${c}b:80`, "/p");
	record(`${c}`, "/p");
}
for (const host of ["localhost", "a\x01b", "a\x7fb", "a\x80b", "a\x9fb", "a\xa0b", "a\xadb"]) record(host, "/p?q");

// A seeded random walk over the characters the parsers branch on, for hosts and targets alike (a linear congruential
// generator, so the corpus is the same on every run).
let seed = 20261109;
const next = (n) => {
	seed = (Math.imul(seed, 1103515245) + 12345) >>> 0;
	return (seed >>> 8) % n;
};
const hostAlphabet = ["a", "B", "z", "0", "1", "7", "9", "x", "X", ".", ".", ":", ":", "@", "[", "]", "%", "4", "1", "f", "-", "_", "/", "\\", "?", "#", "~", "\xe4", "\u00ff", "e", "8", "0"];
const pathAlphabet = ["/", "/", "/", "a", "b", ".", ".", "%", "2", "e", "E", "\\", "?", "#", "&", "=", "^", "`", "{", "}", "'", '"', "<", ">", "|", "~", "0", "z", ":", "@", "[", "]"];
const walk = (alphabet, length) => Array.from({ length }, () => alphabet[next(alphabet.length)]).join("");
for (let i = 0; i < 1500; i++) record("localhost", "/" + walk(pathAlphabet, 1 + next(14)));
for (let i = 0; i < 1500; i++) record(walk(hostAlphabet, 1 + next(12)), "/" + walk(pathAlphabet, next(4)));

process.stdout.write(out.join("\n") + "\n");
