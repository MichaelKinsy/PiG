// Framed socket channel of the Node subprocess (host.mjs): length-prefixed frames of kind 0 (a chord link message,
// Uint8Array) or kind 1 (a control message, JSON object).
import { EventEmitter } from "node:events";

const KIND_LINK = 0;
const KIND_CONTROL = 1;

/** A framed socket as the IPC channel `ipcLink` takes: Uint8Array messages are the link's, anything else the app's. */
export class FramedPort extends EventEmitter {
	#socket;
	#buffer = Buffer.alloc(0);
	/** The frames whose write has not completed, and whether `disconnect` waits for them to end the socket. */
	#writing = 0;
	#ending = false;
	connected = true;

	constructor(socket) {
		super();
		this.#socket = socket;
		socket.on("data", (chunk) => this.#read(chunk));
		const ended = () => {
			if (!this.connected) return;
			this.connected = false;
			this.emit("disconnect");
		};
		socket.on("close", ended);
		socket.on("error", ended);
	}

	#read(chunk) {
		this.#buffer = this.#buffer.length === 0 ? chunk : Buffer.concat([this.#buffer, chunk]);
		for (;;) {
			if (this.#buffer.length < 4) return;
			const length = this.#buffer.readUInt32BE(0);
			if (this.#buffer.length < 4 + length) return;
			const kind = this.#buffer[4];
			const payload = this.#buffer.subarray(5, 4 + length);
			this.#buffer = this.#buffer.subarray(4 + length);
			if (kind === KIND_LINK) this.emit("message", new Uint8Array(payload));
			else if (kind === KIND_CONTROL) this.emit("message", JSON.parse(payload.toString("utf8")));
		}
	}

	#write(kind, payload, callback) {
		// One socket.write per frame: on Windows named pipes libuv's uv_try_write always returns UV_EAGAIN so the first
		// write stays asynchronous in Writable, buffering a second write in JS memory until the first completes; a
		// process.exit() right after the send then delivers only the 5-byte header and loses the frame.
		const frame = Buffer.allocUnsafe(5 + payload.length);
		frame.writeUInt32BE(1 + payload.length, 0);
		frame[4] = kind;
		frame.set(payload, 5);
		this.#writing++;
		return this.#socket.write(frame, (error) => {
			this.#writing--;
			if (this.#ending && this.#writing === 0) this.#socket.end();
			callback(error ?? null);
		});
	}

	send(message, callback) {
		return this.#write(KIND_LINK, message, callback);
	}

	control(message, callback = () => {}) {
		return this.#write(KIND_CONTROL, Buffer.from(JSON.stringify(message)), callback);
	}

	/** Ends the writing side once every frame written is out: an `end()` while a write is in flight loses it (Windows). */
	disconnect() {
		this.connected = false;
		this.#ending = true;
		if (this.#writing === 0) this.#socket.end();
	}
}
