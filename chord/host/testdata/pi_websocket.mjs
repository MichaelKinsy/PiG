// Regenerates pi_websocket.json: Pi's packages/chord/src/websocket.ts (acceptWebSockets) run over a fake server, sockets
// and host. Run from the repository root with Node 24 or later:
//
//	node chord/host/testdata/pi_websocket.mjs > chord/host/testdata/pi_websocket.json
//
// Each case is how the authenticator settles for a request; the record holds what Pi sent on the socket (text frames),
// how it closed the socket, whether it accepted the link on the host and with which kind and principal.
import { acceptWebSockets } from "../../../.upstream/current/packages/chord/src/websocket.ts";

const outcomes = [
	{ request: "/?token=ok", principal: { guest: "web" } },
	{ request: "/?token=bad", principal: null },
	{ request: "/?token=error", throws: "token service down" },
	{ request: "/?token=sync", principal: { guest: "sync" }, sync: true },
	{ request: "/?token=falsy", principal: 0 },
];

const listeners = [];
const server = { on: (event, listener) => { if (event === "connection") listeners.push(listener); } };
const accepted = [];
const host = { accept: (link, options) => accepted.push({ principal: options.principal, kind: options.kind, maxCalls: options.maxCalls ?? null, keys: Object.keys(options).sort() }) };
acceptWebSockets(server, host, {
	maxCalls: 7,
	principal: (request) => {
		const outcome = outcomes.find((item) => item.request === request);
		if (outcome.throws) return Promise.reject(new Error(outcome.throws));
		const value = outcome.principal === null ? undefined : outcome.principal;
		return outcome.sync ? value : Promise.resolve(value);
	},
});

const records = [];
for (const outcome of outcomes) {
	const sent = [];
	const closes = [];
	const socket = {
		readyState: 1,
		send: (data) => sent.push(typeof data === "string" ? data : "<binary>"),
		close: (code, reason) => { closes.push([code ?? null, reason ?? null]); socket.readyState = 3; },
		addEventListener: () => {},
	};
	const before = accepted.length;
	for (const listener of listeners) listener(socket, outcome.request);
	const sentDuringConnect = sent.length + closes.length;
	await new Promise((resolve) => setTimeout(resolve, 10));
	records.push({
		request: outcome.request,
		sentDuringConnect,
		sent,
		closes,
		accepted: accepted.slice(before),
	});
}
process.stdout.write(`${JSON.stringify({ records }, null, "\t")}\n`);
