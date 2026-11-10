// Records which bytes of a request target Node's HTTP parser accepts. Each line is [target, reached]: the request
// `GET <target> HTTP/1.1` with a `Host` header was sent as raw bytes to a Node `http` server, and reached is whether its request
// handler ran (false: the parser answered 400 itself). A target is a string of Latin-1 characters, one byte each.
//
//   node target_probe.cjs > target_corpus.jsonl      (the pinned Node: .node-version)
"use strict";
const http = require("node:http");
const net = require("node:net");

const targets = [];
for (let code = 0; code < 256; code++) {
	if (code === 0x0d || code === 0x0a) continue;
	const c = String.fromCharCode(code);
	// `/ ` is left out: the request line `GET /  HTTP/1.1` (two spaces) Node's parser takes as `/`, Go's net/http refuses.
	targets.push(`/a${c}b`, ...(code === 0x20 ? [] : [`/${c}`]), `/a?b${c}c`, `/a#b${c}c`);
}
targets.push("/", "*", "/a b", "/a\tb", "http://localhost/a", "//a", "/%zz", "/a%", "/\\", "/a/../b");

let reached = false;
const server = http.createServer((_req, res) => {
	reached = true;
	res.end("ok");
});
const exchange = (port, target) =>
	new Promise((resolve) => {
		reached = false;
		const socket = net.connect(port, "127.0.0.1");
		socket.on("data", () => {});
		socket.on("error", () => {});
		socket.on("close", () => resolve(reached));
		socket.write(Buffer.from(`GET ${target} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n`, "latin1"));
	});
server.listen(0, "127.0.0.1", async () => {
	const { port } = server.address();
	const lines = [];
	for (const target of targets) lines.push(JSON.stringify([target, await exchange(port, target)]));
	server.close();
	process.stdout.write(lines.join("\n") + "\n");
});
