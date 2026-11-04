// The worker thread of callbacks.mjs's `callSync`: it owns the second connection to the Go side and answers the host thread
// through a shared integer (`state`, set to 1 with a notify) and a message port (`results`). It runs one request at a time, as
// the host thread that blocks on it does.
import { connect } from "node:net";
import { parentPort, workerData } from "node:worker_threads";

const { address, token, state, results } = workerData;
const KIND_CONTROL = 1;

const socket = connect(address);
let buffer = Buffer.alloc(0);
/** Whether a request is waiting for its answer. */
let waiting = false;
let connected = false;

function frame(message) {
	const payload = Buffer.from(JSON.stringify(message));
	const header = Buffer.allocUnsafe(5);
	header.writeUInt32BE(1 + payload.length, 0);
	header[4] = KIND_CONTROL;
	return Buffer.concat([header, payload]);
}

function settle(message) {
	waiting = false;
	results.postMessage(message);
	Atomics.store(state, 0, 1);
	Atomics.notify(state, 0);
}

socket.once("connect", () => {
	connected = true;
	socket.write(frame({ event: "sync", token }));
	parentPort.postMessage("ready");
});
socket.on("data", (chunk) => {
	buffer = buffer.length === 0 ? chunk : Buffer.concat([buffer, chunk]);
	for (;;) {
		if (buffer.length < 4) return;
		const length = buffer.readUInt32BE(0);
		if (buffer.length < 4 + length) return;
		const reply = JSON.parse(buffer.subarray(5, 4 + length).toString("utf8"));
		buffer = buffer.subarray(4 + length);
		if (waiting) settle(reply);
	}
});
const ended = (error) => {
	// Before the connection was made the starting host thread is not blocked: its `startSync` rejects with this.
	if (!connected) throw new Error(`the synchronous connection failed${error ? `: ${error.message}` : ""}`);
	if (waiting) settle({ ok: false, error: { name: "Error", message: `the synchronous connection ended${error ? `: ${error.message}` : ""}` } });
};
socket.on("close", () => ended());
socket.on("error", ended);

parentPort.on("message", (request) => {
	waiting = true;
	socket.write(frame(request));
});
