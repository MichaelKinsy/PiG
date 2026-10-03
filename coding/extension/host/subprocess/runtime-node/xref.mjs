// pig additive (D19): cross-process references (xref). One Node process is one realm. The realm exports its own objects by reference and imports foreign references as proxies whose every operation runs in the owning realm through the synchronous host call path. Wire shape: plans/0.3.x/gap-xproc.md section 3.
import { randomUUID } from "node:crypto";
import { inspect } from "node:util";
import { runInNewContext } from "node:vm";
import v8 from "node:v8";
import { syncBuiltinESMExports } from "node:module";
import { types as utilTypes } from "node:util";
import { BroadcastChannel, MessagePort, Worker } from "node:worker_threads";

const WELL_KNOWN_SYMBOLS = new Map();
for (const name of Object.getOwnPropertyNames(Symbol)) {
  if (typeof Symbol[name] === "symbol") WELL_KNOWN_SYMBOLS.set(Symbol[name], name);
}

// Intrinsic prototypes and constructors resolve to the reader's own intrinsics, as they are one object in Pi's single heap. Intrinsic methods are not mapped: a method must run in the owner so internal slots resolve.
const INTRINSICS = new Map();
const INTRINSIC_NAMES = new Map();
{
  const constructors = { Object, Function, Array, Error, TypeError, RangeError, SyntaxError, ReferenceError, EvalError, URIError, AggregateError, Map, Set, WeakMap, WeakSet, Date, RegExp, Promise, Boolean, Number, String, Symbol, BigInt, ArrayBuffer, DataView, Uint8Array, Int8Array, Uint16Array, Int16Array, Uint32Array, Int32Array, Float32Array, Float64Array, BigInt64Array, BigUint64Array, Uint8ClampedArray, WeakRef, FinalizationRegistry };
  const add = (name, value) => {
    if (value === undefined || value === null || INTRINSIC_NAMES.has(value)) return;
    INTRINSICS.set(name, value);
    INTRINSIC_NAMES.set(value, name);
  };
  for (const [name, value] of Object.entries(constructors)) {
    add(`%${name}%`, value);
    add(`%${name}.prototype%`, value.prototype);
  }
  add("%TypedArray%", Object.getPrototypeOf(Uint8Array));
  add("%TypedArray.prototype%", Object.getPrototypeOf(Uint8Array.prototype));
  add("%AsyncFunction.prototype%", Object.getPrototypeOf(async function () {}));
  add("%GeneratorFunction.prototype%", Object.getPrototypeOf(function* () {}));
  add("%AsyncGeneratorFunction.prototype%", Object.getPrototypeOf(async function* () {}));
  add("%IteratorPrototype%", Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]())));
  add("%ArrayIteratorPrototype%", Object.getPrototypeOf([][Symbol.iterator]()));
  add("%MapIteratorPrototype%", Object.getPrototypeOf(new Map()[Symbol.iterator]()));
  add("%SetIteratorPrototype%", Object.getPrototypeOf(new Set()[Symbol.iterator]()));
  add("%StringIteratorPrototype%", Object.getPrototypeOf(""[Symbol.iterator]()));
  add("%GeneratorPrototype%", Object.getPrototypeOf(Object.getPrototypeOf((function* () {})())));
  add("%AsyncGeneratorPrototype%", Object.getPrototypeOf(Object.getPrototypeOf((async function* () {})())));
}

// Node's util.format("%s") prints an object with inspect unless it has a user-defined toString or Symbol.toPrimitive (hasBuiltInToString in Node 26's lib/internal/util/inspect.js). The owner decides, when Node asks, with the same rule.
// Node collects the names when its inspect module loads, before Buffer, URL and the other host globals exist: the engine's own globals are the same set.
const BUILTIN_CONSTRUCTORS = new Set(runInNewContext("Object.getOwnPropertyNames(globalThis)").filter(name => /^[A-Z][a-zA-Z0-9]+$/.test(name)));
function hasCustomToString(value) {
  try {
    let hasOwnToString = Object.hasOwn;
    let hasOwnToPrimitive = Object.hasOwn;
    if (typeof value.toString !== "function") {
      if (typeof value[Symbol.toPrimitive] !== "function") return false;
      if (Object.hasOwn(value, Symbol.toPrimitive)) return true;
      hasOwnToString = () => false;
    } else if (Object.hasOwn(value, "toString")) {
      return true;
    } else if (typeof value[Symbol.toPrimitive] !== "function") {
      hasOwnToPrimitive = () => false;
    } else if (Object.hasOwn(value, Symbol.toPrimitive)) {
      return true;
    }
    let pointer = value;
    do { pointer = Object.getPrototypeOf(pointer); } while (!hasOwnToString(pointer, "toString") && !hasOwnToPrimitive(pointer, Symbol.toPrimitive));
    const descriptor = Object.getOwnPropertyDescriptor(pointer, "constructor");
    return !(descriptor !== undefined && typeof descriptor.value === "function" && BUILTIN_CONSTRUCTORS.has(descriptor.value.name));
  } catch {
    return false;
  }
}

const CUSTOM_TO_STRING = { toString() { return ""; } }.toString;

const PROXY_TRAPS = ["get", "set", "has", "deleteProperty", "ownKeys", "getOwnPropertyDescriptor", "defineProperty", "getPrototypeOf", "setPrototypeOf", "isExtensible", "preventExtensions", "apply", "construct"];

// V8 words a rejected assignment, delete, Object.defineProperty or Object.setPrototypeOf in the caller's realm. The owner repeats the operation as a strict caller would, so the message and the failure are the owner object's own.
function strictSet(target, key, value) { target[key] = value; }
function strictDelete(target, key) { delete target[key]; }
function strictDefine(target, key, descriptor) { Object.defineProperty(target, key, descriptor); }
function strictSetPrototype(target, prototype) { Object.setPrototypeOf(target, prototype); }
const STRICT_OPERATIONS = { setStrict: strictSet, deleteStrict: strictDelete, defineStrict: strictDefine, setPrototypeStrict: strictSetPrototype };
const V8_REJECTION = /^(?:Cannot |.+ is not extensible$|Cyclic __proto__ value$|Immutable prototype object )/;

// The owner serializes its own object graph with its own v8.serialize, which resolves any foreign references the graph holds. A clone failure travels as text so the caller raises the class Pi's caller sees.
const CLONE_FAILURE = /could not be cloned/;
function cloneOutcome(serialize) {
  try {
    return { bytes: serialize().toString("base64") };
  } catch (error) {
    if (error instanceof Error && CLONE_FAILURE.test(error.message)) return { cloneError: error.message };
    throw error;
  }
}

// A native pi.events listener receives the emitter's JSON.stringify view, computed here in the owner (one read of each getter): the JSON text, null when JSON has no form for the value, or the error JSON.stringify threw.
function jsonOutcome(value) {
  try {
    const text = JSON.stringify(value);
    return text === undefined ? { json: null } : { json: text };
  } catch (error) {
    return { error: String(error instanceof Error ? error.message : error) };
  }
}

const isBuiltinSite = site => site.getFileName() == null && site.getLineNumber() == null && !site.isEval();

// Reports whether a rejected operation on the proxy throws in its caller: a strict-mode assignment or delete, Object.defineProperty and the builtins that throw on failure. Sloppy code and the Reflect functions observe the false result instead. `trap` is the handler function V8 called.
function throwsInCaller(trap) {
  const holder = {};
  const { prepareStackTrace, stackTraceLimit } = Error;
  let sites;
  try {
    Error.stackTraceLimit = 1;
    Error.prepareStackTrace = (_error, frames) => frames;
    Error.captureStackTrace(holder, trap);
    sites = holder.stack;
  } finally {
    Error.prepareStackTrace = prepareStackTrace;
    Error.stackTraceLimit = stackTraceLimit;
  }
  const site = Array.isArray(sites) ? sites[0] : undefined;
  if (!site) return false;
  if (isBuiltinSite(site)) {
    const name = site.getFunctionName();
    if (site.getTypeName() === "Object" && name === "defineProperty") return true;
    return !(name === "set" || name === "deleteProperty" || name === "defineProperty" || site.getTypeName() === "Reflect");
  }
  // CallSite.getFunction() is undefined for strict-mode code.
  return site.getFunction() === undefined;
}

// Runs a strict operation and returns true, or the message of V8's rejection. An error that user code threw propagates unchanged.
function strictOutcome(operation, args) {
  try {
    operation(...args);
    return true;
  } catch (error) {
    const message = v8RejectionMessage(error, operation);
    if (message === undefined) throw error;
    return { typeError: message };
  }
}

function v8RejectionMessage(error, operation) {
  if (!(error instanceof TypeError) || !V8_REJECTION.test(error.message)) return undefined;
  let sites;
  const { prepareStackTrace } = Error;
  try {
    Error.prepareStackTrace = (_error, frames) => frames;
    sites = error.stack;
  } finally {
    Error.prepareStackTrace = prepareStackTrace;
  }
  if (!Array.isArray(sites)) return undefined;
  const generated = sites.length > 0 && (sites[0].getFunctionName() === operation.name || (isBuiltinSite(sites[0]) && sites[1]?.getFunctionName() === operation.name));
  // Reading the stack formatted it with the structured frames; restore the text Node would have produced.
  if (!generated) error.stack = `${error.name}: ${error.message}${sites.map(site => `\n    at ${site}`).join("")}`;
  return generated ? error.message : undefined;
}

// util.inspect prints a foreign object as Node prints the shadow's inspection result. The owner describes its object so the reader can hand Node a local view of the same kind, properties and prototype; Node then formats the view with its own indentation, seen list and depth, as it formats the object in Pi's one heap.
const isIndexKey = key => typeof key === "string" && /^(?:0|[1-9]\d*)$/.test(key) && Number(key) < 4294967295;
// pig divergence (D83): Node reads a Promise's state, an iterator's position, a module namespace and the like from V8 internals with no public accessor, so the owner formats those and they do not count as a nesting level of the enclosing layout.
const OPAQUE_TYPES = ["isPromise", "isSharedArrayBuffer", "isArgumentsObject", "isGeneratorObject", "isMapIterator", "isSetIterator", "isModuleNamespaceObject", "isExternal"];
// A view copies at most this many bytes of a typed array's buffer; a larger buffer is formatted by the owner.
const VIEW_BUFFER_LIMIT = 64 * 1024 * 1024;
const TYPED_ARRAY = Object.getPrototypeOf(Uint8Array);
const typedArrayName = Reflect.getOwnPropertyDescriptor(TYPED_ARRAY.prototype, Symbol.toStringTag).get;

const bufferViewName = value => (utilTypes.isDataView(value) ? "DataView" : typedArrayName.call(value));

function isDetached(buffer) {
  try {
    new Uint8Array(buffer);
    return false;
  } catch {
    return true;
  }
}

// The kinds Node formats by an internal slot are mirrored by a local object of the same kind. A kind whose state only V8 internals expose is formatted by the owner.
function viewKind(value) {
  if (utilTypes.isProxy(value)) return "opaque";
  if (typeof value === "function") return "function";
  if (Array.isArray(value)) return "array";
  if (utilTypes.isMap(value)) return "map";
  if (utilTypes.isSet(value)) return "set";
  if (utilTypes.isDate(value)) return "date";
  if (utilTypes.isRegExp(value)) return "regexp";
  if (utilTypes.isNativeError(value)) return "error";
  if (utilTypes.isBoxedPrimitive(value)) return "boxed";
  if (utilTypes.isWeakMap(value)) return "weakmap";
  if (utilTypes.isWeakSet(value)) return "weakset";
  if (utilTypes.isArrayBuffer(value)) return isDetached(value) || value.byteLength > VIEW_BUFFER_LIMIT ? "opaque" : "arraybuffer";
  if (utilTypes.isArrayBufferView(value)) {
    const buffer = value.buffer;
    return !utilTypes.isArrayBuffer(buffer) || isDetached(buffer) || buffer.byteLength > VIEW_BUFFER_LIMIT || typeof globalThis[bufferViewName(value)] !== "function" ? "opaque" : "typedarray";
  }
  return OPAQUE_TYPES.some(check => utilTypes[check](value)) ? "opaque" : "object";
}

function boxedValue(value) {
  if (utilTypes.isNumberObject(value)) return Number.prototype.valueOf.call(value);
  if (utilTypes.isStringObject(value)) return String.prototype.valueOf.call(value);
  if (utilTypes.isBooleanObject(value)) return Boolean.prototype.valueOf.call(value);
  if (utilTypes.isBigIntObject(value)) return BigInt.prototype.valueOf.call(value);
  return Symbol.prototype.valueOf.call(value);
}

// The custom inspection function Node would call for the value, by Node's own conditions (formatValue in lib/internal/util/inspect.js).
function customInspectOf(value) {
  const custom = value[inspect.custom];
  if (typeof custom !== "function" || custom === inspect || Object.getOwnPropertyDescriptor(value, "constructor")?.value?.prototype === value) return undefined;
  return custom;
}

export class Realm {
  // transport() returns an object with call(method, args) (synchronous host call) and notify(method, args), or undefined when no connection exists.
  constructor(transport) {
    this.id = randomUUID();
    this.transport = transport;
    this.nextId = 0;
    this.exports = new Map(); // id -> { value, sent }; value is an object, function or unique symbol
    this.exportIds = new WeakMap(); // object/function/unique symbol -> id
    this.imports = new Map(); // "realm:id" -> entry { key, weak, delivered }
    this.refs = new WeakMap(); // proxy or imported unique symbol -> { realm, id, kind, desc }
    this.releases = [];
    this.releaseScheduled = false;
    this.registry = new FinalizationRegistry(entry => this.finalized(entry));
    // The prototype of every proxy's shadow target. It is created here so its hook captures no proxy's scope.
    const realm = this;
    this.shadows = new WeakMap(); // shadow target -> ref
    this.views = new WeakMap(); // proxy -> { signature, view }
    // util.format("%s") asks Node's hasBuiltInToString about the shadow. The shadow has no toString, so a foreign object with a user-defined toString or Symbol.toPrimitive is reported by an accessor that asks the owner when Node reads it: Node then sees the user-defined toString it sees in Pi, however late the owner added it.
    this.inspector = Object.create(null, {
      [inspect.custom]: { configurable: true, enumerable: false, writable: true, value: function (depth, options, inspectArg) { return realm.inspectView(this, depth, options, inspectArg); } },
      toString: { configurable: true, enumerable: false, get() { return realm.customToString(this) ? CUSTOM_TO_STRING : undefined; } },
      [Symbol.toPrimitive]: { configurable: true, enumerable: false, get() { return undefined; } },
    });
  }

  customToString(shadow) {
    const ref = this.shadows.get(shadow);
    return ref !== undefined && this.call(ref, "customToString", []) === true;
  }

  encode(value) {
    switch (typeof value) {
      case "undefined": return { $x: "undefined" };
      case "boolean": case "string": return value;
      case "number":
        if (Number.isNaN(value)) return { $x: "number", v: "NaN" };
        if (value === Infinity) return { $x: "number", v: "Infinity" };
        if (value === -Infinity) return { $x: "number", v: "-Infinity" };
        if (Object.is(value, -0)) return { $x: "number", v: "-0" };
        return value;
      case "bigint": return { $x: "bigint", v: value.toString() };
      case "symbol": return this.encodeSymbol(value);
      default: break;
    }
    if (value === null) return null;
    const intrinsic = INTRINSIC_NAMES.get(value);
    if (intrinsic !== undefined) return { $x: "intrinsic", name: intrinsic };
    const imported = this.refs.get(value);
    if (imported) return { $x: "ref", realm: imported.realm, id: imported.id, kind: imported.kind };
    let id = this.exportIds.get(value);
    let entry = id === undefined ? undefined : this.exports.get(id);
    if (!entry) {
      id = String(++this.nextId);
      entry = { value, sent: 0 };
      this.exports.set(id, entry);
      this.exportIds.set(value, id);
    }
    entry.sent++;
    return { $x: "ref", realm: this.id, id, kind: typeof value === "function" ? "function" : Array.isArray(value) ? "array" : "object" };
  }

  // A unique symbol has an object's lifetime: the owner exports it under an id counted and unpinned like an object's, and an importer holds its stand-in weakly and releases each transmission when it is collected.
  encodeSymbol(symbol) {
    const wellKnown = WELL_KNOWN_SYMBOLS.get(symbol);
    if (wellKnown !== undefined) return { $x: "symbol", wk: wellKnown };
    const key = Symbol.keyFor(symbol);
    if (key !== undefined) return { $x: "symbol", for: key };
    const foreign = this.refs.get(symbol);
    if (foreign) return { $x: "symbol", realm: foreign.realm, id: foreign.id, desc: foreign.desc };
    let id = this.exportIds.get(symbol);
    let entry = id === undefined ? undefined : this.exports.get(id);
    if (!entry) {
      id = String(++this.nextId);
      entry = { value: symbol, sent: 0 };
      this.exports.set(id, entry);
      this.exportIds.set(symbol, id);
    }
    entry.sent++;
    return { $x: "symbol", realm: this.id, id, desc: symbol.description ?? null };
  }

  decode(value) {
    if (value === null || typeof value !== "object") return value;
    switch (value.$x) {
      case "undefined": return undefined;
      case "number": return value.v === "-0" ? -0 : Number(value.v);
      case "bigint": return BigInt(value.v);
      case "json": return value.v;
      case "intrinsic": {
        if (!INTRINSICS.has(value.name)) throw new Error(`Unknown cross-process intrinsic: ${value.name}`);
        return INTRINSICS.get(value.name);
      }
      case "symbol": return this.decodeSymbol(value);
      case "ref": return this.decodeRef(value);
      default: throw new Error("Malformed cross-process value");
    }
  }

  decodeSymbol(value) {
    if (value.wk !== undefined) return Symbol[value.wk];
    if (value.for !== undefined) return Symbol.for(value.for);
    if (value.realm === this.id) {
      const entry = this.exports.get(value.id);
      if (!entry || typeof entry.value !== "symbol") throw new Error(`Unknown cross-process symbol: ${value.id}`);
      return entry.value;
    }
    const key = `${value.realm}:${value.id}`;
    const existing = this.imports.get(key);
    const known = existing?.weak.deref();
    if (known !== undefined) {
      existing.delivered++;
      return known;
    }
    const ref = { realm: value.realm, id: value.id, kind: "symbol", desc: value.desc ?? null };
    const symbol = Symbol(value.desc ?? undefined);
    const entry = { key, ref, weak: new WeakRef(symbol), delivered: 1 };
    this.imports.set(key, entry);
    this.refs.set(symbol, ref);
    this.registry.register(symbol, entry);
    return symbol;
  }

  decodeRef(value) {
    if (value.realm === this.id) {
      const entry = this.exports.get(value.id);
      if (!entry) throw new Error(`Unknown cross-process reference: ${value.id}`);
      return entry.value;
    }
    const key = `${value.realm}:${value.id}`;
    const existing = this.imports.get(key);
    const proxy = existing?.weak.deref();
    if (proxy !== undefined) {
      existing.delivered++;
      return proxy;
    }
    const ref = { realm: value.realm, id: value.id, kind: value.kind };
    const created = this.createProxy(ref);
    const entry = { key, ref, weak: new WeakRef(created), delivered: 1 };
    this.imports.set(key, entry);
    this.refs.set(created, ref);
    this.registry.register(created, entry);
    return created;
  }

  // Each transmission the Host delivered is released exactly once, by the proxy entry that accounted for it.
  // pig divergence (D83): a proxy kept alive by a foreign object that it keeps alive in turn is released only when one realm exits.
  finalized(entry) {
    if (this.imports.get(entry.key) === entry) this.imports.delete(entry.key);
    this.releases.push({ realm: entry.ref.realm, id: entry.ref.id, count: entry.delivered });
    this.scheduleRelease();
  }

  scheduleRelease() {
    if (this.releaseScheduled) return;
    this.releaseScheduled = true;
    setImmediate(() => this.flushReleases()).unref?.();
  }

  flushReleases() {
    this.releaseScheduled = false;
    if (this.releases.length === 0) return;
    const transport = this.transport();
    if (!transport) return;
    const items = this.releases.splice(0);
    try { transport.notify("xref.release", { items }); } catch {}
  }

  unpin({ items }) {
    for (const { id, count } of items ?? []) {
      const entry = this.exports.get(id);
      if (!entry) continue;
      entry.sent -= count;
      if (entry.sent <= 0) {
        this.exports.delete(id);
        this.exportIds.delete(entry.value);
      }
    }
  }

  // An importer that exits holds no transmissions; the Host sends the counts it retained for that realm as an ordinary unpin.
  stats() {
    let live = 0;
    for (const entry of this.imports.values()) if (entry.weak.deref() !== undefined) live++;
    return { exports: this.exports.size, imports: this.imports.size, liveImports: live, pendingReleases: this.releases.length };
  }

  call(ref, op, args) {
    const transport = this.transport();
    if (!transport) throw new Error("cross-process reference used without an extension connection");
    const result = transport.call("xref.op", { ref: { realm: ref.realm, id: ref.id }, op, args });
    if (result && Object.hasOwn(result, "threw")) throw this.decode(result.threw);
    return this.decode(result?.value ?? null);
  }

  // Copies of foreign objects made by their owners with the owner's own v8.serialize, so internal slots resolve, in `proxies` order. One request per owner keeps sharing between the copies.
  foreignCopies(proxies, makeError) {
    const groups = new Map();
    for (const proxy of proxies) {
      const realm = this.refs.get(proxy).realm;
      if (groups.has(realm)) groups.get(realm).push(proxy);
      else groups.set(realm, [proxy]);
    }
    const copies = new Map();
    for (const group of groups.values()) {
      const outcome = this.call(this.refs.get(group[0]), "serialize", group.map(proxy => this.encode(proxy)));
      if (outcome.cloneError !== undefined) throw makeError(outcome.cloneError);
      // v8.deserialize builds arrays in dictionary mode, which serialize differently from the arrays Pi's caller holds; rebuild them with ordinary elements.
      const des = v8.deserialize(Buffer.from(outcome.bytes, "base64"));
      const values = materialize(this, des, new Set(), () => true, makeError).finish();
      group.forEach((proxy, index) => copies.set(proxy, values[index]));
    }
    return copies;
  }

  // [[Get]] of a property through a foreign object for a receiver that is another object: the owner finds the holder, and a property inherited from an intrinsic prototype resolves in this realm on the receiver, so an accessor such as Map.prototype.size sees the receiver's internal slots as it does in Pi's single heap.
  lookup(ref, key, receiver) {
    const found = this.call(ref, "lookup", [this.encodeKey(key)]);
    if (found.intrinsic !== undefined) return Reflect.get(INTRINSICS.get(found.intrinsic), key, receiver);
    if (found.descriptor === undefined) return undefined;
    const descriptor = this.decodeDescriptor(found.descriptor);
    if (Object.hasOwn(descriptor, "value")) return descriptor.value;
    return descriptor.get === undefined ? undefined : Reflect.apply(descriptor.get, receiver, []);
  }

  // Runs a strict operation on a foreign object and throws V8's TypeError, created here so its stack starts at the caller, when the owner rejects it.
  strict(ref, op, args, trap) {
    const outcome = this.call(ref, op, args);
    if (outcome === true) return true;
    const error = new TypeError(outcome.typeError);
    Error.captureStackTrace(error, trap);
    throw error;
  }

  encodeKey(key) { return typeof key === "symbol" ? this.encodeSymbol(key) : key; }
  decodeKey(key) { return typeof key === "string" ? key : this.decodeSymbol(key); }

  encodeDescriptor(descriptor) {
    if (descriptor === undefined) return undefined;
    const out = {};
    for (const field of ["writable", "enumerable", "configurable"]) if (Object.hasOwn(descriptor, field)) out[field] = Boolean(descriptor[field]);
    for (const field of ["value", "get", "set"]) if (Object.hasOwn(descriptor, field)) out[field] = this.encode(descriptor[field]);
    return out;
  }

  decodeDescriptor(descriptor) {
    if (descriptor === undefined || descriptor === null) return undefined;
    const out = {};
    for (const field of ["writable", "enumerable", "configurable"]) if (Object.hasOwn(descriptor, field)) out[field] = descriptor[field];
    for (const field of ["value", "get", "set"]) if (Object.hasOwn(descriptor, field)) out[field] = this.decode(descriptor[field]);
    return out;
  }

  // serve executes one operation on an own object for a foreign realm.
  serve({ ref, op, args = [] }) {
    const entry = ref?.realm === this.id ? this.exports.get(ref.id) : undefined;
    if (!entry) throw new Error(`Unknown cross-process reference: ${ref?.id}`);
    const target = entry.value;
    try {
      return { value: this.encodeResult(op, this.perform(target, op, args)) };
    } catch (error) {
      return { threw: this.encode(error) };
    }
  }

  perform(target, op, args) {
    // Decode every transferred value exactly once, before the operation can throw, so each delivered reference is accounted for.
    const defining = op === "defineProperty" || op === "defineStrict";
    const descriptor = defining ? this.decodeDescriptor(args[1]) : undefined;
    const values = defining ? [this.decode(args[0])] : op === "inspect" || op === "view" ? [] : args.map(arg => this.decode(arg));
    switch (op) {
      case "get": return Reflect.get(target, values[0]);
      case "set": return values.length > 2 ? Reflect.set(target, values[0], values[1], values[2]) : Reflect.set(target, values[0], values[1]);
      case "has": return Reflect.has(target, values[0]);
      case "deleteProperty": return Reflect.deleteProperty(target, values[0]);
      case "ownKeys": return Reflect.ownKeys(target);
      case "getOwnPropertyDescriptor": return Reflect.getOwnPropertyDescriptor(target, values[0]);
      case "defineProperty": return Reflect.defineProperty(target, values[0], descriptor);
      case "getPrototypeOf": return Reflect.getPrototypeOf(target);
      case "setPrototypeOf": return Reflect.setPrototypeOf(target, values[0]);
      case "isExtensible": return Reflect.isExtensible(target);
      case "preventExtensions": return Reflect.preventExtensions(target);
      case "apply": return Reflect.apply(target, values[0], values.slice(1));
      case "construct": return Reflect.construct(target, values.slice(1), values[0]);
      case "inspect": {
        const options = {};
        for (const [key, value] of Object.entries(args[0]?.v ?? {})) options[key] = this.decode(value);
        return inspect(target, options);
      }
      case "lookup": {
        for (let holder = target; holder !== null; holder = Reflect.getPrototypeOf(holder)) {
          if (holder !== target && INTRINSIC_NAMES.has(holder)) return { intrinsic: INTRINSIC_NAMES.get(holder) };
          const descriptor = Reflect.getOwnPropertyDescriptor(holder, values[0]);
          if (descriptor !== undefined) return { descriptor: this.encodeDescriptor(descriptor) };
        }
        return {};
      }
      case "view": return this.viewSnapshot(target, args[0]?.v?.limit);
      case "customToString": return hasCustomToString(target);
      case "functionText": return Function.prototype.toString.call(target);
      case "serialize": return cloneOutcome(() => v8.serialize(values));
      case "serializeOne": return cloneOutcome(() => v8.serialize(values[0]));
      case "json": return jsonOutcome(target);
      case "detach": return structuredClone(values[0], { transfer: [values[0]] }) && true;
      case "setStrict": return strictOutcome(strictSet, [target, values[0], values[1]]);
      case "deleteStrict": return strictOutcome(strictDelete, [target, values[0]]);
      case "defineStrict": return strictOutcome(strictDefine, [target, values[0], descriptor]);
      case "setPrototypeStrict": return strictOutcome(strictSetPrototype, [target, values[0]]);
      case "snapshotDescriptors": {
        const out = [];
        for (const key of Reflect.ownKeys(target)) out.push([this.encodeKey(key), this.encodeDescriptor(Reflect.getOwnPropertyDescriptor(target, key))]);
        return { descriptors: out, prototype: this.encode(Reflect.getPrototypeOf(target)) };
      }
      default: throw new TypeError(`Unknown cross-process operation: ${op}`);
    }
  }

  // Describes the object for a local view: its kind and internal-slot contents (at most `limit` entries of an array, Map or Set, as Node reads no more; -1 reads all), own property descriptors in ownKeys order, and prototype. `custom` is the inspection function Node would call.
  viewSnapshot(target, limit = -1) {
    const kind = viewKind(target);
    const custom = utilTypes.isProxy(target) ? undefined : customInspectOf(target);
    const out = { kind, custom: custom === undefined ? null : this.encode(custom) };
    if (kind === "opaque") return out;
    out.prototype = this.encode(Reflect.getPrototypeOf(target));
    const keep = limit < 0 ? Infinity : limit;
    let keys = Reflect.ownKeys(target);
    switch (kind) {
      case "array": {
        out.length = target.length;
        let indexes = 0;
        keys = keys.filter(key => key !== "length" && (!isIndexKey(key) || indexes++ < keep));
        break;
      }
      case "map": {
        out.size = Reflect.get(Map.prototype, "size", target);
        out.entries = [];
        for (const [key, entry] of Map.prototype.entries.call(target)) {
          if (out.entries.length >= keep) break;
          out.entries.push([this.encode(key), this.encode(entry)]);
        }
        break;
      }
      case "set": {
        out.size = Reflect.get(Set.prototype, "size", target);
        out.entries = [];
        for (const entry of Set.prototype.values.call(target)) {
          if (out.entries.length >= keep) break;
          out.entries.push(this.encode(entry));
        }
        break;
      }
      case "function":
        // Node names a class by the text of its source and reads its own properties.
        out.klass = inspect(target, { customInspect: false, depth: 0, breakLength: Infinity }).startsWith("[class ");
        out.generator = utilTypes.isGeneratorFunction(target);
        out.async = utilTypes.isAsyncFunction(target);
        out.constructible = Object.hasOwn(target, "prototype");
        break;
      case "arraybuffer": out.bytes = Buffer.from(new Uint8Array(target, 0, Math.min(target.byteLength, keep))).toString("base64"); out.byteLength = target.byteLength; break;
      case "typedarray": {
        const buffer = target.buffer;
        const width = utilTypes.isDataView(target) ? 1 : target.BYTES_PER_ELEMENT;
        const items = utilTypes.isDataView(target) ? 0 : Math.min(target.length, keep);
        out.type = bufferViewName(target);
        out.byteLength = buffer.byteLength;
        out.byteOffset = target.byteOffset;
        out.length = utilTypes.isDataView(target) ? target.byteLength : target.length;
        out.bytes = Buffer.from(new Uint8Array(buffer, 0, Math.min(buffer.byteLength, Math.max(keep, target.byteOffset + items * width)))).toString("base64");
        keys = keys.filter(key => !isIndexKey(key));
        break;
      }
      case "date": out.time = this.encode(Date.prototype.getTime.call(target)); break;
      case "regexp":
        out.source = Reflect.get(RegExp.prototype, "source", target);
        out.flags = Reflect.get(RegExp.prototype, "flags", target);
        break;
      case "boxed":
        out.boxed = this.encode(boxedValue(target));
        if (utilTypes.isStringObject(target)) keys = keys.filter(key => key !== "length" && !isIndexKey(key));
        break;
      default: break;
    }
    out.descriptors = keys.map(key => [this.encodeKey(key), this.encodeDescriptor(Reflect.getOwnPropertyDescriptor(target, key))]);
    return out;
  }

  encodeResult(op, result) {
    if (op === "ownKeys") return { $x: "json", v: result.map(key => this.encodeKey(key)) };
    if (op === "getOwnPropertyDescriptor") return result === undefined ? { $x: "undefined" } : { $x: "json", v: this.encodeDescriptor(result) };
    if (op === "snapshotDescriptors" || op === "view" || op === "lookup" || op === "serialize" || op === "serializeOne" || op === "json" || Object.hasOwn(STRICT_OPERATIONS, op)) return { $x: "json", v: result };
    return this.encode(result);
  }

  // pig divergence (D83): a foreign object is a proxy, so internal-slot brand checks fail in the reader.
  createProxy(ref) {
    const realm = this;
    const shadow = ref.kind === "array" ? [] : ref.kind === "function" ? function () {}.bind(null) : {};
    // util.inspect formats the innermost non-proxy target of a proxy, so the shadow answers with the hook. The hook lives on the shadow's prototype, not as an own key, so a non-extensible shadow reports exactly the owner's own keys.
    Reflect.setPrototypeOf(shadow, this.inspector);
    this.shadows.set(shadow, ref);
    let proxy;
    const receiverArg = receiver => (receiver === proxy ? [] : [realm.encode(receiver)]);
    // A non-extensible owner requires the shadow to hold the same own properties and prototype before V8 checks trap invariants.
    const freezeShadow = () => {
      if (!Reflect.isExtensible(shadow)) return;
      const { descriptors, prototype } = realm.call(ref, "snapshotDescriptors", []);
      for (const [key, descriptor] of descriptors) Reflect.defineProperty(shadow, realm.decodeKey(key), realm.decodeDescriptor(descriptor));
      Reflect.setPrototypeOf(shadow, realm.decode(prototype));
      Reflect.preventExtensions(shadow);
    };
    const syncDescriptor = (key, descriptor) => {
      if (descriptor && descriptor.configurable === false) {
        const current = Reflect.getOwnPropertyDescriptor(shadow, key);
        if (!current || current.configurable !== false || current.writable !== descriptor.writable || current.value !== descriptor.value) Reflect.defineProperty(shadow, key, descriptor);
      }
    };
    const handler = {
      get(_target, key, receiver) {
        if (receiver === proxy) return realm.call(ref, "get", [realm.encodeKey(key)]);
        return realm.lookup(ref, key, receiver);
      },
      set(_target, key, value, receiver) {
        if (receiver === proxy && throwsInCaller(handler.set)) return realm.strict(ref, "setStrict", [realm.encodeKey(key), realm.encode(value)], handler.set);
        return realm.call(ref, "set", [realm.encodeKey(key), realm.encode(value), ...receiverArg(receiver)]);
      },
      has(_target, key) { return realm.call(ref, "has", [realm.encodeKey(key)]); },
      deleteProperty(_target, key) {
        if (throwsInCaller(handler.deleteProperty)) return realm.strict(ref, "deleteStrict", [realm.encodeKey(key)], handler.deleteProperty);
        return realm.call(ref, "deleteProperty", [realm.encodeKey(key)]);
      },
      ownKeys() { return realm.call(ref, "ownKeys", []).map(key => realm.decodeKey(key)); },
      getOwnPropertyDescriptor(_target, key) {
        const encoded = realm.call(ref, "getOwnPropertyDescriptor", [realm.encodeKey(key)]);
        const descriptor = realm.decodeDescriptor(encoded);
        syncDescriptor(key, descriptor);
        return descriptor;
      },
      defineProperty(_target, key, descriptor) {
        const encoded = [realm.encodeKey(key), realm.encodeDescriptor(descriptor)];
        const ok = throwsInCaller(handler.defineProperty) ? realm.strict(ref, "defineStrict", encoded, handler.defineProperty) : realm.call(ref, "defineProperty", encoded);
        if (ok) syncDescriptor(key, descriptor.configurable === false ? realm.decodeDescriptor(realm.call(ref, "getOwnPropertyDescriptor", [realm.encodeKey(key)])) : undefined);
        return ok;
      },
      getPrototypeOf() { return realm.call(ref, "getPrototypeOf", []); },
      setPrototypeOf(_target, prototype) {
        if (throwsInCaller(handler.setPrototypeOf)) return realm.strict(ref, "setPrototypeStrict", [realm.encode(prototype)], handler.setPrototypeOf);
        return realm.call(ref, "setPrototypeOf", [realm.encode(prototype)]);
      },
      isExtensible() {
        const extensible = realm.call(ref, "isExtensible", []);
        if (!extensible) freezeShadow();
        return extensible;
      },
      preventExtensions() {
        const ok = realm.call(ref, "preventExtensions", []);
        if (ok) freezeShadow();
        return ok;
      },
      apply(_target, thisArg, args) { return realm.call(ref, "apply", [realm.encode(thisArg), ...args.map(arg => realm.encode(arg))]); },
      construct(_target, args, newTarget) { return realm.call(ref, "construct", [realm.encode(newTarget), ...args.map(arg => realm.encode(arg))]); },
    };
    for (const trap of PROXY_TRAPS) if (!handler[trap]) throw new Error(`missing trap ${trap}`);
    proxy = new Proxy(shadow, handler);
    return proxy;
  }

  // util.inspect formats a proxy's shadow target, so the shadow's prototype hook lands here with the proxy as receiver. The owner's inspection function, when it has one, runs on the owner's object as it does in Pi. Otherwise the hook returns a local view of the owner's object, which Node formats with the caller's indentation, seen list, depth and options, and a kind Node cannot mirror is formatted by the owner.
  inspectView(proxy, depth, options, inspectArg) {
    const ref = this.refs.get(proxy);
    if (!ref) return "";
    const max = options?.maxArrayLength;
    const snapshot = this.call(ref, "view", [{ $x: "json", v: { limit: typeof max !== "number" || max === Infinity ? -1 : Number.isNaN(max) ? 0 : Math.ceil(Math.max(0, max)) } }]);
    const decoded = this.decodeView(snapshot);
    if (decoded.custom !== undefined) {
      const returned = Reflect.apply(decoded.custom, proxy, [depth, Object.assign({}, options), inspectArg]);
      if (returned !== proxy) return returned;
    }
    // Node lists the entries of a weak collection and the prototype of a class under showHidden, which a local view cannot supply.
    const hidden = options?.showHidden === true && (snapshot.kind === "weakmap" || snapshot.kind === "weakset" || snapshot.klass === true);
    if (snapshot.kind === "opaque" || decoded.custom !== undefined || hidden) return this.inspectOwned(ref, depth, options);
    const signature = JSON.stringify(snapshot);
    const cached = this.views.get(proxy);
    // The same view answers every visit of one unchanged object, so Node's seen list finds a cycle through it.
    if (cached?.signature === signature) return cached.view;
    const view = this.buildView(proxy, snapshot, decoded);
    this.views.set(proxy, { signature, view });
    return view;
  }

  inspectOwned(ref, depth, options) {
    const encoded = {};
    for (const [key, value] of Object.entries(options ?? {})) if (key !== "stylize") encoded[key] = this.encode(value);
    encoded.depth = this.encode(depth);
    return this.call(ref, "inspect", [{ $x: "json", v: encoded }]);
  }

  // Decodes every transferred value, so each delivered reference is accounted for.
  decodeView(snapshot) {
    const decoded = { custom: snapshot.custom === null ? undefined : this.decode(snapshot.custom) };
    if (snapshot.kind === "opaque") return decoded;
    decoded.prototype = this.decode(snapshot.prototype);
    decoded.entries = snapshot.entries?.map(entry => (snapshot.kind === "map" ? entry.map(item => this.decode(item)) : this.decode(entry)));
    if (snapshot.time !== undefined) decoded.time = this.decode(snapshot.time);
    if (snapshot.boxed !== undefined) decoded.boxed = this.decode(snapshot.boxed);
    decoded.descriptors = snapshot.descriptors.map(([key, descriptor]) => [this.decodeKey(key), this.decodeDescriptor(descriptor)]);
    return decoded;
  }

  // A local object of the owner object's kind, with its own properties (an accessor reads the owner's property) and its prototype. An array, Map or Set holds only the entries Node reads and is padded to the owner's length or size.
  buildView(proxy, snapshot, decoded) {
    let view;
    switch (snapshot.kind) {
      case "array": view = []; break;
      case "function":
        if (snapshot.klass) view = class {};
        else if (snapshot.generator) view = snapshot.async ? async function* () {} : function* () {};
        else if (snapshot.async) view = snapshot.constructible ? async function () {} : async () => {};
        else view = snapshot.constructible ? function () {} : () => {};
        break;
      case "weakmap": view = new WeakMap(); break;
      case "weakset": view = new WeakSet(); break;
      case "arraybuffer": {
        view = new ArrayBuffer(snapshot.byteLength);
        new Uint8Array(view).set(Buffer.from(snapshot.bytes, "base64"));
        break;
      }
      case "typedarray": {
        const buffer = new ArrayBuffer(snapshot.byteLength);
        new Uint8Array(buffer).set(Buffer.from(snapshot.bytes, "base64"));
        view = new globalThis[snapshot.type](buffer, snapshot.byteOffset, snapshot.length);
        break;
      }
      case "map": {
        view = new Map(decoded.entries);
        for (let index = view.size; index < snapshot.size; index++) view.set(Symbol(), undefined);
        break;
      }
      case "set": {
        view = new Set(decoded.entries);
        for (let index = view.size; index < snapshot.size; index++) view.add(Symbol());
        break;
      }
      case "date": view = new Date(decoded.time); break;
      case "regexp": view = new RegExp(snapshot.source, snapshot.flags); break;
      case "error": view = new Error(); break;
      case "boxed": view = Object(decoded.boxed); break;
      default: view = {}; break;
    }
    for (const [key, descriptor] of decoded.descriptors) {
      const existing = Reflect.getOwnPropertyDescriptor(view, key);
      if (existing !== undefined && existing.configurable === false) {
        if (Object.hasOwn(descriptor, "value") && existing.writable) view[key] = descriptor.value;
        continue;
      }
      if (Object.hasOwn(descriptor, "value") || Object.hasOwn(descriptor, "writable")) {
        Reflect.defineProperty(view, key, descriptor);
        continue;
      }
      const accessor = { enumerable: descriptor.enumerable, configurable: descriptor.configurable };
      if (descriptor.get !== undefined) accessor.get = function () { return Reflect.get(proxy, key); };
      if (descriptor.set !== undefined) accessor.set = function (value) { Reflect.set(proxy, key, value); };
      Reflect.defineProperty(view, key, accessor);
    }
    if (snapshot.kind === "object" || snapshot.kind === "error" || snapshot.kind === "function") {
      const owned = new Set(decoded.descriptors.map(([key]) => key));
      for (const key of Reflect.ownKeys(view)) if (!owned.has(key)) Reflect.deleteProperty(view, key);
    }
    if (snapshot.kind === "array") view.length = snapshot.length;
    Reflect.setPrototypeOf(view, decoded.prototype);
    return view;
  }
}

class Placeholder {
  constructor(proxy) { this.proxy = proxy; }
}

// Objects whose structured clone is decided by an internal slot: they hold no foreign reference of their own and stay in the graph for the native clone.
const CLONE_LEAF_TYPES = ["isDate", "isRegExp", "isNativeError", "isBoxedPrimitive", "isAnyArrayBuffer", "isArrayBufferView", "isPromise", "isWeakMap", "isWeakSet", "isProxy", "isGeneratorObject", "isMapIterator", "isSetIterator", "isModuleNamespaceObject", "isExternal"];

// Finds, without running a getter or a proxy trap, the local objects whose structured-clone graph reaches a foreign object through data properties, array elements and Map or Set entries, or may reach one through a getter. Only those need rebuilding: every other object stays in the graph, so the native clone sees its host objects and transfer rules exactly as Pi's clone does.
function foreignScan(realm) {
  const parents = new Map();
  const dirty = new Set();
  const scanned = new Set();
  const mark = object => {
    const stack = [object];
    while (stack.length > 0) {
      const next = stack.pop();
      if (dirty.has(next)) continue;
      dirty.add(next);
      for (const parent of parents.get(next) ?? []) stack.push(parent);
    }
  };
  const scan = root => {
    const stack = [root];
    while (stack.length > 0) {
      const value = stack.pop();
      if (scanned.has(value)) continue;
      scanned.add(value);
      const edge = child => {
        if ((typeof child !== "object" && typeof child !== "function") || child === null) return;
        if (realm.refs.has(child)) {
          mark(value);
          return;
        }
        if (typeof child === "function") return;
        let list = parents.get(child);
        if (!list) parents.set(child, (list = []));
        list.push(value);
        if (dirty.has(child)) mark(value);
        else if (!scanned.has(child)) stack.push(child);
      };
      if (realm.refs.has(value) || typeof value !== "object" || value === null) continue;
      if (utilTypes.isMap(value)) {
        for (const [key, entry] of Map.prototype.entries.call(value)) { edge(key); edge(entry); }
        continue;
      }
      if (utilTypes.isSet(value)) {
        for (const entry of Set.prototype.values.call(value)) edge(entry);
        continue;
      }
      if (CLONE_LEAF_TYPES.some(check => utilTypes[check](value))) continue;
      for (const key of Object.keys(value)) {
        const descriptor = Reflect.getOwnPropertyDescriptor(value, key);
        if (descriptor === undefined) continue;
        // A getter's result is unknown until the clone reads it, so the object is rebuilt with a getter that reads it then.
        if (Object.hasOwn(descriptor, "value")) edge(descriptor.value);
        else mark(value);
      }
    }
  };
  // A value first met through a getter of a rebuilt object is scanned when it is met.
  return value => {
    if (!scanned.has(value)) scan(value);
    return dirty.has(value);
  };
}

// Rebuilds the local part of a value's structured-clone graph with each foreign object replaced by a placeholder, reading properties in structured-clone order once. Only objects that `reaches` a foreign object are rebuilt. A getter of a rebuilt object is not called here: the copy has a getter that calls it when the native clone reads that property, in the native clone's order, and rebuilds what it returns the same way. `settle` fetches the owners' copies of the placeholders met so far and puts them in place.
function materialize(realm, root, keep, reaches, makeError) {
  const memo = new Map();
  const placeholders = new Map();
  const copies = new Map();
  let slots = [];
  const resolve = value => (value instanceof Placeholder ? copies.get(value.proxy) : value);
  // Array indices are assigned so the copy keeps ordinary elements; other keys are defined so "__proto__" stays an own property.
  const define = (container, key, value) => {
    if (Array.isArray(container) && /^(?:0|[1-9]\d*)$/.test(key)) {
      const index = Number(key);
      if (index === container.length) container.push(value);
      else container[index] = value;
    }
    else Object.defineProperty(container, key, { value, writable: true, enumerable: true, configurable: true });
  };
  const put = (container, key, child) => {
    const walked = walk(child);
    define(container, key, walked);
    if (walked instanceof Placeholder) slots.push(() => define(container, key, copies.get(walked.proxy)));
  };
  const settle = () => {
    const pending = [...placeholders.keys()].filter(proxy => !copies.has(proxy));
    if (pending.length > 0) for (const [proxy, copy] of realm.foreignCopies(pending, makeError)) copies.set(proxy, copy);
    const run = slots;
    slots = [];
    for (const slot of run) slot();
  };
  const lazy = (container, key, holder, getter) => {
    Object.defineProperty(container, key, {
      enumerable: true,
      configurable: true,
      get: getter === undefined ? undefined : function () {
        const walked = walk(Reflect.apply(getter, holder, []));
        settle();
        return resolve(walked);
      },
    });
  };
  const copyKeys = (container, value, keys) => {
    for (const key of keys) {
      const descriptor = Reflect.getOwnPropertyDescriptor(value, key);
      if (descriptor !== undefined && !Object.hasOwn(descriptor, "value")) lazy(container, key, value, descriptor.get);
      else put(container, key, value[key]);
    }
  };
  const walk = value => {
    if ((typeof value !== "object" && typeof value !== "function") || value === null) return value;
    if (realm.refs.has(value)) {
      let placeholder = placeholders.get(value);
      if (!placeholder) placeholders.set(value, (placeholder = new Placeholder(value)));
      return placeholder;
    }
    if (typeof value === "function" || keep.has(value)) return value;
    if (memo.has(value)) return memo.get(value);
    if (!reaches(value)) return value;
    if (Array.isArray(value)) {
      const keys = Object.keys(value);
      let dense = keys.length >= value.length;
      for (let index = 0; dense && index < value.length; index++) dense = keys[index] === String(index);
      // V8 records the elements kind of the arrays a literal creates and starts later ones from it, and v8.serialize writes a holey array in its sparse form. A copy of a dense array is built at a site that only ever holds dense arrays.
      const out = dense ? [] : new Array(value.length);
      memo.set(value, out);
      copyKeys(out, value, keys);
      if (out.length !== value.length) out.length = value.length;
      return out;
    }
    if (utilTypes.isMap(value)) {
      const out = new Map();
      memo.set(value, out);
      for (const [key, entry] of Map.prototype.entries.call(value)) {
        const walkedKey = walk(key);
        const walkedEntry = walk(entry);
        slots.push(() => out.set(resolve(walkedKey), resolve(walkedEntry)));
      }
      return out;
    }
    if (utilTypes.isSet(value)) {
      const out = new Set();
      memo.set(value, out);
      for (const entry of Set.prototype.values.call(value)) {
        const walkedEntry = walk(entry);
        slots.push(() => out.add(resolve(walkedEntry)));
      }
      return out;
    }
    if (CLONE_LEAF_TYPES.some(check => utilTypes[check](value))) return value;
    const out = {};
    memo.set(value, out);
    copyKeys(out, value, Object.keys(value));
    return out;
  };
  const walked = walk(root);
  return {
    foreign: [...placeholders.keys()],
    finish() {
      settle();
      return resolve(walked);
    },
  };
}

// The function each wrapper stands for; Function.prototype.toString prints that function's source, as a program sees for Node's own function.
const DISGUISED = new WeakMap();
const namedLike = (wrapper, native) => {
  Object.defineProperty(wrapper, "name", { value: native.name, configurable: true });
  Object.defineProperty(wrapper, "length", { value: native.length, configurable: true });
  DISGUISED.set(wrapper, native);
  return wrapper;
};

// pig divergence (D83): a foreign object is a proxy, which structuredClone, v8.serialize and postMessage reject. When a value reaches a foreign object, these wrappers clone the foreign objects in their owners, rebuild only the local objects that hold them and run the native function on the result, so the caller gets what Pi's caller gets from the object in one heap. Every other value goes to the native function unchanged.
export function installCloneHooks(realm) {
  const nativeFunctionToString = Function.prototype.toString;
  // A foreign function has no source of its own here: its owner prints it.
  Function.prototype.toString = namedLike({
    toString() {
      const foreign = realm.refs.get(this);
      if (foreign !== undefined && typeof this === "function") return realm.call(foreign, "functionText", []);
      const stands = DISGUISED.get(this);
      return Reflect.apply(nativeFunctionToString, stands === undefined ? this : stands, []);
    },
  }.toString, nativeFunctionToString);
  const nativeClone = globalThis.structuredClone;
  const nativeWriteValue = v8.Serializer.prototype.writeValue;
  // v8.serialize as Node defines it, over the unpatched writeValue.
  const nativeSerialize = value => {
    const serializer = new v8.DefaultSerializer();
    serializer.writeHeader();
    Reflect.apply(nativeWriteValue, serializer, [value]);
    return serializer.releaseBuffer();
  };
  const serializeName = v8.serialize;
  const dataCloneError = message => new DOMException(message, "DataCloneError");
  const plainError = message => new Error(message);
  const transferList = transfer => {
    try { return transfer === undefined || transfer === null ? [] : Array.from(transfer); } catch { return []; }
  };
  // Returns the value with foreign objects replaced by their owners' copies and the transfer list without foreign entries, or undefined when the graph holds no foreign object and no getter. `detach` detaches the foreign transfer entries in their owners; the caller runs it once the native function has accepted the message, as a rejected message transfers nothing.
  const prepare = (value, transfer, makeError) => {
    const foreignTransfers = transfer.filter(item => realm.refs.has(item));
    const local = transfer.filter(item => !realm.refs.has(item));
    const prepared = materialize(realm, value, new Set(local), foreignScan(realm), makeError).finish();
    if (prepared === value && foreignTransfers.length === 0) return undefined;
    const detach = () => {
      for (const item of foreignTransfers) realm.call(realm.refs.get(item), "detach", [realm.encode(item)]);
    };
    return { value: prepared, transfer: local, detach };
  };
  // The native function reads the graph's getters itself, in its own order, so each getter runs once and a graph the native function rejects reads nothing after the rejected value; a foreign object a getter returns is copied when it is read.
  const withForeign = (value, transfer, makeError, run) => {
    const prepared = prepare(value, transfer, makeError);
    if (!prepared) return run(undefined);
    const result = run(prepared);
    prepared.detach();
    return result;
  };
  const isOptions = options => options === undefined || options === null || typeof options === "object";

  globalThis.structuredClone = namedLike(function structuredClone(...args) {
    const options = args[1];
    if (realm.imports.size === 0 || args.length === 0 || !isOptions(options)) return Reflect.apply(nativeClone, undefined, args);
    return withForeign(args[0], transferList(options?.transfer), dataCloneError, prepared => {
      if (!prepared) return Reflect.apply(nativeClone, undefined, args);
      const next = options === undefined || options === null ? options : { ...options, transfer: prepared.transfer };
      return Reflect.apply(nativeClone, undefined, [prepared.value, next]);
    });
  }, nativeClone);

  v8.serialize = namedLike(function serialize(...args) {
    const value = args[0];
    if (realm.imports.size === 0) return nativeSerialize(value);
    if (realm.refs.has(value)) {
      const outcome = realm.call(realm.refs.get(value), "serializeOne", [realm.encode(value)]);
      if (outcome.cloneError !== undefined) throw plainError(outcome.cloneError);
      return Buffer.from(outcome.bytes, "base64");
    }
    return withForeign(value, [], plainError, prepared => nativeSerialize(prepared ? prepared.value : value));
  }, serializeName);

  // A failed write leaves partial bytes in the serializer, so a graph that reaches a foreign object is prepared before it is written; any other graph is written unchanged.
  // writeValue and postMessage are native methods: the wrappers are methods too, with no prototype and no [[Construct]].
  v8.Serializer.prototype.writeValue = namedLike({
    writeValue(...args) {
      if (realm.imports.size === 0) return Reflect.apply(nativeWriteValue, this, args);
      const prepared = prepare(args[0], [], plainError);
      return Reflect.apply(nativeWriteValue, this, prepared ? [prepared.value, ...args.slice(1)] : args);
    },
  }.writeValue, nativeWriteValue);

  for (const prototype of [MessagePort.prototype, Worker.prototype, BroadcastChannel.prototype]) {
    const native = prototype.postMessage;
    if (typeof native !== "function") continue;
    prototype.postMessage = namedLike({
      postMessage(...args) {
        const second = args[1];
        if (realm.imports.size === 0 || args.length === 0) return Reflect.apply(native, this, args);
        const transfer = transferList(Array.isArray(second) ? second : second?.transfer);
        return withForeign(args[0], transfer, dataCloneError, prepared => {
          if (!prepared) return Reflect.apply(native, this, args);
          const next = Array.isArray(second) ? prepared.transfer : second !== null && typeof second === "object" ? { ...second, transfer: prepared.transfer } : second;
          return Reflect.apply(native, this, args.length > 1 ? [prepared.value, next] : [prepared.value]);
        });
      },
    }.postMessage, native);
  }
  syncBuiltinESMExports();
}
