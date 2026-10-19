// Event streams: the host sends the first payload of an event name whole and every later one as operations on the payload this connection last received (EventStream in the host's protocol.go). A runtime keeps one replica per event name and key (a tool_execution_update has one for each tool call), applies the operations as the frame arrives, in arrival order, and hands each handler its own copy.

export function cloneStreamValue(value) {
  if (Array.isArray(value)) return value.map(cloneStreamValue);
  if (value === null || typeof value !== "object") return value;
  const copy = {};
  for (const key of Object.keys(value)) Object.defineProperty(copy, key, { value: cloneStreamValue(value[key]), writable: true, enumerable: true, configurable: true });
  return copy;
}

function setStreamMember(parent, key, value) {
  if (Array.isArray(parent)) {
    if (!Number.isInteger(key) || key < 0 || key > parent.length) throw new Error(`event stream: bad array index ${key}`);
    parent[key] = value;
  } else if (parent !== null && typeof parent === "object" && typeof key === "string") {
    Object.defineProperty(parent, key, { value, writable: true, enumerable: true, configurable: true });
  } else {
    throw new Error("event stream: bad path");
  }
}

export function applyStreamOps(root, ops) {
  if (!Array.isArray(ops)) throw new Error("event stream: ops are not an array");
  for (const op of ops) {
    const { path, value } = op;
    if (!Array.isArray(path)) throw new Error("event stream: bad path");
    if (path.length === 0) {
      if (op.op !== "set") throw new Error("event stream: bad root operation");
      root = value;
      continue;
    }
    let parent = root;
    for (let index = 0; index < path.length - 1; index++) {
      if (parent === null || typeof parent !== "object") throw new Error("event stream: bad path");
      parent = Object.prototype.hasOwnProperty.call(parent, path[index]) ? parent[path[index]] : undefined;
    }
    if (parent === null || typeof parent !== "object") throw new Error("event stream: bad path");
    const last = path[path.length - 1];
    if (op.op === "append") {
      const current = Object.prototype.hasOwnProperty.call(parent, last) ? parent[last] : undefined;
      if (typeof current !== "string" || typeof value !== "string") throw new Error("event stream: append to a non-string");
      setStreamMember(parent, last, current + value);
    } else if (op.op === "set") {
      setStreamMember(parent, last, value);
    } else {
      throw new Error(`event stream: unknown operation ${op.op}`);
    }
  }
  return root;
}

export class EventReplicas {
  #replicas = new Map();

  // apply replaces request.args with the full payload request.stream produces, or sets request.streamError. A frame's evict names keys of the event whose replicas the runtime drops once it applied the frame.
  apply(request) {
    const { seq, base = 0, ops, key = "", evict } = request.stream;
    const id = `${request.event}\0${key}`;
    try {
      if (request.args !== undefined) {
        this.#replicas.set(id, { seq, value: request.args });
      } else {
        const replica = this.#replicas.get(id);
        if (!replica || replica.seq !== base) throw new Error(`${request.event} delta applies to payload ${base}, the replica holds ${replica?.seq}`);
        replica.value = applyStreamOps(replica.value, ops);
        replica.seq = seq;
      }
      request.args = cloneStreamValue(this.#replicas.get(id).value);
    } catch (error) {
      this.#replicas.delete(id);
      request.streamError = error instanceof Error ? error : new Error(String(error));
    }
    if (Array.isArray(evict)) for (const evicted of evict) this.#replicas.delete(`${request.event}\0${evicted}`);
  }
}
