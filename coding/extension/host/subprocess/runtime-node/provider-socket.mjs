// pig additive (D19): one IO worker owns the extension's existing socket while synchronous Provider methods wait off the host UI loop.
import { EventEmitter } from "node:events";
import { FrameBuffer } from "./frame-buffer.mjs";
import net from "node:net";
import { isMainThread, MessageChannel, receiveMessageOnPort, Worker, workerData } from "node:worker_threads";

const MAX_FRAME_SIZE = 128 * 1024 * 1024;
const parseJSON = JSON.parse;
const sockets = new Set();
const wake = new Int32Array(workerData?.providerSocket ? workerData.wake : new SharedArrayBuffer(Int32Array.BYTES_PER_ELEMENT));
let waitDepth = 0;

// isWaiting reports whether the main thread is inside a synchronous host wait, where queued microtasks cannot run until the wait returns.
export function isWaiting() {
  return waitDepth > 0;
}

if (!isMainThread && workerData?.providerSocket) {
  const { port, path, shared } = workerData;
  const state = new Int32Array(shared);
  let unread = 0;
  const post = message => {
    port.postMessage(message);
    Atomics.add(state, 0, 1);
    Atomics.notify(state, 0);
    Atomics.add(wake, 0, 1);
    Atomics.notify(wake, 0);
  };
  const socket = net.createConnection(path, () => post({ kind: "ready", highWaterMark: socket.writableHighWaterMark }));
  socket.on("error", error => post({ kind: "error", message: error.message }));
  socket.on("close", () => {
    post({ kind: "close" });
    port.close();
  });
  socket.on("drain", () => { Atomics.store(state, 2, 0); post({ kind: "drain" }); });
  const frames = new FrameBuffer(MAX_FRAME_SIZE, body => {
    const json = body.toString("utf8");
    const envelope = parseJSON(json);
    if (envelope.type === "ping") {
      const response = Buffer.from(JSON.stringify({ type: "pong", pong: { nonce: envelope.ping?.nonce ?? "" } }));
      const header = Buffer.alloc(4); header.writeUInt32BE(response.length);
      socket.write(Buffer.concat([header, response]));
    } else {
      unread += body.length + 4;
      // Validate on the IO worker, but forward text rather than structured-cloning the decoded graph. The main thread decodes the same text with its captured native parser.
      post({ kind: "envelope", json, bytes: body.length + 4 });
    }
  });
  socket.on("data", chunk => {
    try {
      frames.write(chunk);
      if (unread >= MAX_FRAME_SIZE) socket.pause();
    } catch (error) {
      socket.destroy(error);
    }
  });
  port.on("message", message => {
    if (message.kind === "write") {
      const data = Buffer.from(message.data);
      if (!socket.write(data, () => {
        Atomics.sub(state, 1, data.length);
        post({ kind: "drain" });
      })) Atomics.store(state, 2, 1);
    } else if (message.kind === "read") {
      unread -= message.bytes;
      if (unread < MAX_FRAME_SIZE) socket.resume();
    } else if (message.kind === "close") socket.destroy();
  });
  process.on("exit", () => {
    Atomics.store(state, 3, 1);
    Atomics.add(state, 0, 1);
    Atomics.notify(state, 0);
    Atomics.add(wake, 0, 1);
    Atomics.notify(wake, 0);
  });
}

// setProviderSocketsRef makes the host transports keep this process alive,
// or lets the event loop drain while they stay open.
export function setProviderSocketsRef(ref) {
  for (const socket of sockets) {
    if (ref) {
      socket.worker.ref();
      socket.port.ref();
    } else {
      socket.worker.unref();
      socket.port.unref();
    }
  }
}

export class ProviderSocket extends EventEmitter {
  static async connect(path) {
    const socket = new ProviderSocket(path);
    await new Promise((resolve, reject) => {
      socket.once("ready", resolve);
      socket.once("error", reject);
    });
    return socket;
  }

  constructor(path) {
    super();
    this.state = new Int32Array(new SharedArrayBuffer(4 * Int32Array.BYTES_PER_ELEMENT));
    const { port1, port2 } = new MessageChannel();
    this.port = port1;
    this.worker = new Worker(new URL(import.meta.url), {
      workerData: { providerSocket: true, path, port: port2, shared: this.state.buffer, wake: wake.buffer },
      transferList: [port2],
      // The transport loads only this trusted module, not the parent's eval/loader bootstrap.
      execArgv: [],
    });
    this.port.on("message", message => this.deliver(message));
    this.worker.on("error", error => this.emit("error", error));
    // Worker exit can precede delivery from its transferred MessagePort. Drain posted envelopes and errors before closing that port.
    this.worker.on("exit", () => { this.pump(); this.finish(); });
    sockets.add(this);
  }

  finish() {
    if (this.closed) return;
    this.closed = true;
    sockets.delete(this);
    this.emit("close");
    this.port.close();
  }

  deliver(message) {
    if (message.kind === "envelope") {
      this.port.postMessage({ kind: "read", bytes: message.bytes });
      this.emit("envelope", parseJSON(message.json));
    } else if (message.kind === "ready") {
      this.highWaterMark = message.highWaterMark;
      this.emit("ready");
    } else if (message.kind === "error") this.emit("error", new Error(message.message));
    else if (message.kind === "close") this.finish();
    else if (message.kind === "drain") this.emit("drain");
  }

  pump() {
    let packet;
    while ((packet = receiveMessageOnPort(this.port))) this.deliver(packet.message);
    if (Atomics.load(this.state, 3)) this.finish();
  }

  waitUntil(complete) {
    waitDepth++;
    try {
      while (!complete()) {
        const sequence = Atomics.load(wake, 0);
        // A synchronous host operation may call another member of this packed cell. Service each socket's restricted synchronous dispatcher, without running arbitrary async handlers reentrantly.
        for (const socket of sockets) socket.pump();
        if (complete()) return;
        if (this.closed) throw new Error("connection closed");
        Atomics.wait(wake, 0, sequence);
      }
    } finally {
      waitDepth--;
    }
  }

  destroy() {
    if (!this.closed) this.port.postMessage({ kind: "close" });
    return this;
  }

  get writableNeedDrain() {
    return Atomics.load(this.state, 1) >= this.highWaterMark || Atomics.load(this.state, 2) !== 0;
  }

  write(data) {
    this.waitUntil(() => Atomics.load(this.state, 1) < MAX_FRAME_SIZE || this.closed);
    if (this.closed) throw new Error("connection closed");
    Atomics.add(this.state, 1, data.length);
    this.port.postMessage({ kind: "write", data });
  }
}
