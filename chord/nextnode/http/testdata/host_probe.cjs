// Records what Node's URL parser makes of a Host with no port, no userinfo and no path: `new URL("http://" + host + "/p").hostname`, or
// null when it throws. Hosts are Unicode strings, as parseHost takes them (after the percent-decoding it does itself), so this
// reaches the characters a Latin-1 Host header cannot hold: the fullwidth and ideographic dots, fullwidth letters, Arabic and Hebrew labels.
// Each line of the output is [host, hostname | null].
//
//   node host_probe.cjs > host_corpus.jsonl      (the pinned Node: .node-version)
"use strict";

const out = [];
function record(host) {
	let result = null;
	try {
		result = new URL(`http://${host}/p`).hostname;
	} catch {}
	out.push(JSON.stringify([host, result]));
}

const fixed = [
	"xn--bcher-kv\u00aa.\u00e4", "xn--bcher-kva%e3%80%82%c3%a4", "xn--%ef%bd%82cher-kva.%c3%a4", "xn--b\uff43her-kva.\u00e4",
	"\u00e4\u3002xn--", "\u00e4\uff0exn--", "\u00e4\uff61xn--", "%c3%a4%e3%80%82xn--", "%c3%a4%ef%bc%8exn--", "%c3%a4%ef%bd%a1xn--",
	"\uff58n--.\u00e4", "%ef%bd%98n--.%c3%a4", "\uff58\uff4e--.\u00e4", "\uff58\uff4e\uff0d\uff0d.\u00e4", "XN--.\u00e4",
	"1.%c3%a4.xn--mgbh0fb", "1.\u00e4.xn--mgbh0fb", "1.xn--mgbh0fb", "a.xn--mgbh0fb", "\u00e4.xn--mgbh0fb", "xn--mgbh0fb.1", "xn--mgbh0fb.a",
	"xn--mgbh0fb.\u00e4", "\u0628.1", "\u0628.a", "1.\u0628", "a.\u0628", "\u0628\u0627.\u00e4", "\u00e4.\u0628\u0627", "\u05d0.\u00e4", "\u05d0.1", "1.\u05d0",
	"\u05d0\u0628", "\u05d01", "\u05d0a", "a\u05d0", "\u0628a", "1\u0628", "\u0628" + "1", "\u0628.\u05d0", "\u0628-.\u05d0", "-\u0628", "\u0628-", "\u0661\u0628", "\u0628\u0661",
	"a\u00adb", "\u00ad", "a.\u00ad.b", "\u00ad.a", "a.\u00ad", "xn--\u00ad", "xn--a\u00ad", "\u00adxn--a",
	"\u00df", "\u00dfa.\u00e4", "\u1e9e", "\uff21\uff22", "a\u200db", "\u00e4\u200d", "\u200d\u00e4", "\u00e4.\u200c.b", "\u0301a", "a.\u0301b", "\u0301.\u00e4",
	"\u00e4.xn--a", "\u00e4.xn--a--", "\u00e4.xn--0", "\u00e4.xn--abc-", "\u00e4.xn---a", "\u00e4.xn--1ug", "\u00e4.xn--mgbh0fb", "\u00e4.xn--4ca", "xn--4ca.\u00e4", "xn--4ca.a",
	"\u00e4.xn--\u00e4", "xn--\u00e4.a", "xn--.\u00e4", "..xn--\u00e4", "\u00e4..xn--", "\u00e4.xn--.", "\u00e4.xn--.a",
];
for (const host of fixed) record(host);

let seed = 20261110;
const next = (n) => {
	seed = (Math.imul(seed, 1103515245) + 12345) >>> 0;
	return (seed >>> 8) % n;
};
const atoms = ["xn--", "xn--", "XN--", "\uff58\uff4e\uff0d\uff0d", "\uff58n--", "a", "b", "A", "\u00aa", "\u00e4", "\u00c4", "\u00df", "-", "1", "9", "0", ".", ".", ".", "\u3002", "\uff0e", "\uff61",
	"xn--mgbh0fb", "xn--4ca", "xn--a", "\u05d0", "\u0628", "\u0627", "\u0661", "\u00ad", "\u200d", "\u200c", "\u0301", "_", "%c3%a4", "%e3%80%82", "%ef%bd%a1", "%78n--", "\uff42", "\uff21"];
const walk = (length) => Array.from({ length }, () => atoms[next(atoms.length)]).join("");
for (let i = 0; i < 2500; i++) record(walk(1 + next(6)));

process.stdout.write(out.join("\n") + "\n");
