// pig additive (D19): one IO worker owns every extension socket of the process while synchronous Provider methods wait off the host UI loop. Each socket keeps its own port, shared state and frame buffer, so the members of a packed Node cell (D20) do not each pay for a V8 isolate.
import { EventEmitter } from "node:events";
import { FrameBuffer } from "./frame-buffer.mjs";
import net from "node:net";
import { isMainThread, MessageChannel, parentPort, receiveMessageOnPort, Worker, workerData } from "node:worker_threads";

const MAX_FRAME_SIZE = 128 * 1024 * 1024;
// The host sends some frames, such as each model registry publication, to every member of a cell. A frame of SHARED_FRAME_MIN bytes or more reaches the main thread as shared bytes with an identity, so the IO worker reads and validates a repeat of it by comparison and the main thread can decode it once for every socket.
const SHARED_FRAME_MIN = 64 * 1024;
// The IO worker compares a large frame with the last SHARED_FRAMES_KEPT distinct ones up to SHARED_FRAME_KEPT_MAX bytes, which covers the host's consecutive registry publications.
const SHARED_FRAMES_KEPT = 2;
const SHARED_FRAME_KEPT_MAX = 16 * 1024 * 1024;
const parseJSON = JSON.parse;
const sockets = new Set();
const wake = new Int32Array(workerData?.providerSocket ? workerData.wake : new SharedArrayBuffer(Int32Array.BYTES_PER_ELEMENT));
let waitDepth = 0;

// isWaiting reports whether the main thread is inside a synchronous host wait, where queued microtasks cannot run until the wait returns.
export function isWaiting() {
  return waitDepth > 0;
}

if (!isMainThread && workerData?.providerSocket) {
  // The state of each socket whose transport has not ended.
  const open = new Set();
  // The last distinct large frames validated, newest first, and the identity of each large frame.
  const kept = [];
  const identities = new WeakMap();
  let frameIDs = 0;
  const keep = bytes => {
    if (bytes.length > SHARED_FRAME_KEPT_MAX || kept[0] === bytes) return;
    const index = kept.indexOf(bytes);
    if (index > 0) kept.splice(index, 1);
    kept.unshift(bytes);
    kept.length = Math.min(kept.length, SHARED_FRAMES_KEPT);
  };
  const repeatOf = size => kept.find(bytes => bytes.length === size);
  parentPort.on("message", message => {
    // The main thread stops the worker once no socket remains, so the worker exits after its last socket as a worker per socket did.
    if (message.kind === "stop") return parentPort.close();
    const { port, path, shared } = message;
    const state = new Int32Array(shared);
    open.add(state);
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
      open.delete(state);
      ended(state);
    });
    socket.on("drain", () => { Atomics.store(state, 2, 0); post({ kind: "drain" }); });
    const frames = new FrameBuffer(MAX_FRAME_SIZE, (body, repeated) => {
      let shared = repeated ? body : body.length >= SHARED_FRAME_MIN ? kept.find(bytes => bytes.equals(body)) : undefined;
      if (shared === undefined) {
        const json = body.toString("utf8");
        const envelope = parseJSON(json);
        if (envelope.type === "ping") {
          const response = Buffer.from(JSON.stringify({ type: "pong", pong: { nonce: envelope.ping?.nonce ?? "" } }));
          const header = Buffer.alloc(4); header.writeUInt32BE(response.length);
          socket.write(Buffer.concat([header, response]));
          return;
        }
        // Validate on the IO worker, but forward text or shared bytes rather than structured-cloning the decoded graph. The main thread decodes them with its captured native parser.
        if (body.length < SHARED_FRAME_MIN) {
          unread += body.length + 4;
          post({ kind: "envelope", json, bytes: body.length + 4 });
          return;
        }
        shared = body;
        identities.set(shared, ++frameIDs);
      }
      keep(shared);
      unread += shared.length + 4;
      post({ kind: "envelope", frame: shared.buffer, id: identities.get(shared), bytes: shared.length + 4 });
    }, { sharedMin: SHARED_FRAME_MIN, repeatOf });
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
  });
  process.on("exit", () => {
    for (const state of open) ended(state);
  });
}

// ended tells a main thread waiting on a socket that its transport is gone.
function ended(state) {
  Atomics.store(state, 3, 1);
  Atomics.add(state, 0, 1);
  Atomics.notify(state, 0);
  Atomics.add(wake, 0, 1);
  Atomics.notify(wake, 0);
}

// The process's IO worker. The first socket starts it and it stops after its last socket ends; a worker that exits ends its sockets, and the next socket starts another.
let transport;
function ioWorker() {
  if (transport) return transport;
  const worker = new Worker(new URL(import.meta.url), {
    workerData: { providerSocket: true, wake: wake.buffer },
    // The transport loads only this trusted module, not the parent's eval/loader bootstrap.
    execArgv: [],
  });
  worker.on("error", error => {
    for (const socket of sockets) if (socket.worker === worker) socket.emit("error", error);
  });
  // Worker exit can precede delivery from a transferred MessagePort. Drain each socket's posted envelopes and errors before closing its port.
  worker.on("exit", () => {
    if (transport === worker) transport = undefined;
    for (const socket of [...sockets]) {
      if (socket.worker !== worker) continue;
      socket.pump();
      socket.finish();
    }
  });
  transport = worker;
  return worker;
}

// holdWorker keeps the process alive through the worker while one of its sockets is referenced, as a worker per socket did. A worker left with no socket stops, held as the socket that ended last was, so the process waits for its exit as it waited for that socket's own worker.
function holdWorker(worker, last) {
  let serving = false;
  let held = false;
  for (const socket of sockets) {
    if (socket.worker !== worker) continue;
    serving = true;
    held ||= socket.referenced;
  }
  if (!serving) {
    held = last?.referenced === true;
    if (transport === worker) transport = undefined;
    worker.postMessage({ kind: "stop" });
  }
  if (held) worker.ref();
  else worker.unref();
}

// setProviderSocketsRef makes the host transports keep this process alive,
// or lets the event loop drain while they stay open.
export function setProviderSocketsRef(ref) {
  for (const socket of sockets) {
    socket.referenced = ref;
    if (ref) socket.port.ref();
    else socket.port.unref();
  }
  if (transport) holdWorker(transport);
}

// A shared frame whose decoded envelope passes the runtime's test (shareDecodedFrames) decodes once: every socket that receives the same frame gets the same envelope, which the runtime reads without changing it and copies before an extension sees any of it. The newest such envelope is held; older ones only while something else still holds them.
let shareable = () => false;
const decodedFrames = [];
const DECODED_FRAMES_KEPT = 4;

export function shareDecodedFrames(accepts) {
  shareable = accepts;
}

function decodeFrame(worker, { frame, id }) {
  for (const decoded of decodedFrames) {
    if (decoded.worker !== worker || decoded.id !== id) continue;
    const envelope = decoded.envelope ?? decoded.held.deref();
    if (envelope !== undefined) return envelope;
  }
  const envelope = parseJSON(Buffer.from(frame).toString("utf8"));
  if (!shareable(envelope)) return envelope;
  for (const decoded of decodedFrames) {
    if (decoded.envelope === undefined) continue;
    decoded.held = new WeakRef(decoded.envelope);
    decoded.envelope = undefined;
  }
  decodedFrames.unshift({ worker, id, envelope });
  decodedFrames.length = Math.min(decodedFrames.length, DECODED_FRAMES_KEPT);
  return envelope;
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
    this.referenced = true;
    this.worker = ioWorker();
    this.worker.postMessage({ kind: "open", path, port: port2, shared: this.state.buffer }, [port2]);
    this.port.on("message", message => this.deliver(message));
    sockets.add(this);
    holdWorker(this.worker);
  }

  finish() {
    if (this.closed) return;
    this.closed = true;
    sockets.delete(this);
    holdWorker(this.worker, this);
    this.emit("close");
    this.port.close();
  }

  deliver(message) {
    if (message.kind === "envelope") {
      this.port.postMessage({ kind: "read", bytes: message.bytes });
      this.emit("envelope", message.frame ? decodeFrame(this.worker, message) : parseJSON(message.json));
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
