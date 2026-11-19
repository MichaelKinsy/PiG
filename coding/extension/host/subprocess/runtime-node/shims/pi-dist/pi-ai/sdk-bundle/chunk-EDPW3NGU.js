import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/utils/json-parse.js
import { parse as partialParse } from "../../../partial-json/dist/index.js";
var VALID_JSON_ESCAPES = /* @__PURE__ */ new Set(['"', "\\", "/", "b", "f", "n", "r", "t"]);
function isControlCharacter(char) {
  const codePoint = char.codePointAt(0);
  return codePoint !== void 0 && codePoint >= 0 && codePoint <= 31;
}
__name(isControlCharacter, "isControlCharacter");
function escapeControlCharacter(char) {
  switch (char) {
    case "\b":
      return "\\b";
    case "\f":
      return "\\f";
    case "\n":
      return "\\n";
    case "\r":
      return "\\r";
    case "	":
      return "\\t";
    default:
      return `\\u${char.codePointAt(0)?.toString(16).padStart(4, "0") ?? "0000"}`;
  }
}
__name(escapeControlCharacter, "escapeControlCharacter");
function repairJson(json) {
  let repaired = "";
  let inString = false;
  for (let index = 0; index < json.length; index++) {
    const char = json[index];
    if (!inString) {
      repaired += char;
      if (char === '"') {
        inString = true;
      }
      continue;
    }
    if (char === '"') {
      repaired += char;
      inString = false;
      continue;
    }
    if (char === "\\") {
      const nextChar = json[index + 1];
      if (nextChar === void 0) {
        repaired += "\\\\";
        continue;
      }
      if (nextChar === "u") {
        const unicodeDigits = json.slice(index + 2, index + 6);
        if (/^[0-9a-fA-F]{4}$/.test(unicodeDigits)) {
          repaired += `\\u${unicodeDigits}`;
          index += 5;
          continue;
        }
      }
      if (VALID_JSON_ESCAPES.has(nextChar)) {
        repaired += `\\${nextChar}`;
        index += 1;
        continue;
      }
      repaired += "\\\\";
      continue;
    }
    repaired += isControlCharacter(char) ? escapeControlCharacter(char) : char;
  }
  return repaired;
}
__name(repairJson, "repairJson");
function parseJsonWithRepair(json) {
  try {
    return JSON.parse(json);
  } catch (error) {
    const repairedJson = repairJson(json);
    if (repairedJson !== json) {
      return JSON.parse(repairedJson);
    }
    throw error;
  }
}
__name(parseJsonWithRepair, "parseJsonWithRepair");
function parseToolCallArguments(json) {
  if (!json?.trim())
    return { arguments: {} };
  try {
    const parsed = parseJsonWithRepair(json);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      throw new Error("Tool arguments must be a JSON object");
    }
    return { arguments: parsed };
  } catch (error) {
    return {
      arguments: {},
      argumentsError: `${error instanceof Error ? error.message : String(error)}

Received arguments:
${json}`
    };
  }
}
__name(parseToolCallArguments, "parseToolCallArguments");
function parseStreamingJson(partialJson) {
  if (!partialJson || partialJson.trim() === "") {
    return {};
  }
  try {
    return parseJsonWithRepair(partialJson);
  } catch {
    try {
      const result = partialParse(partialJson);
      return result ?? {};
    } catch {
      try {
        const result = partialParse(repairJson(partialJson));
        return result ?? {};
      } catch {
        return {};
      }
    }
  }
}
__name(parseStreamingJson, "parseStreamingJson");

// pi-dist/pi-ai/utils/streaming-json.js
var MAX_REPORTED_DEPTH = 32;
var ROOT_SLOT = { kind: "root" };
var CREDIT_PER_APPEND = 256;
var CREDIT_PER_CHARACTER = 8;
var MAX_PENDING_SCALAR_LENGTH = 64;
var NUMBER = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/;
var NUMBER_WITHOUT_EXPONENT_DIGITS = /^(-?(?:0|[1-9]\d*)(?:\.\d+)?)[eE][+-]?$/;
var LITERALS = [
  ["true", true],
  ["false", false],
  ["null", null]
];
var QUOTE = 34;
var BACKSLASH = 92;
var StreamingJsonParser = class {
  static {
    __name(this, "StreamingJsonParser");
  }
  #root = void 0;
  #done = false;
  #stack = [];
  #string = void 0;
  #scalar = "";
  #scalarSlot = void 0;
  /** Sum of `size + 1` over the open path: what a fresh snapshot copies. */
  #pathCost = 0;
  #credit = 0;
  #dirty = false;
  #snapshot = void 0;
  /** Changes of the piece being read. */
  #changes = [];
  /**
   * Read the next piece of text; returns the changes it made, in order. Costs time linear in `text.length`.
   */
  append(text) {
    this.#credit += CREDIT_PER_APPEND + CREDIT_PER_CHARACTER * text.length;
    this.#changes = [];
    if (this.#string !== void 0)
      this.#string.change = void 0;
    let index = 0;
    while (index < text.length && !this.#done) {
      const open = this.#string;
      if (open !== void 0)
        index = this.#readString(open, text, index);
      else if (this.#scalar !== "")
        index = this.#readScalar(text, index);
      else
        this.#structural(text[index++]);
    }
    return this.#changes;
  }
  /**
   * The value of the text read so far: `undefined` before a value starts, otherwise a plain JSON value that never
   * changes afterwards. Returns the previous snapshot while the text read since did not change the value, or while a
   * fresh copy of the open path is not yet paid for (see the module comment).
   */
  snapshot() {
    if (!this.#dirty)
      return this.#snapshot;
    const pending = this.#pendingScalar();
    const cost = this.#pathCost + (pending === void 0 ? 0 : 1);
    if (cost > this.#credit)
      return this.#snapshot;
    this.#credit -= cost;
    this.#dirty = false;
    this.#snapshot = this.#copyOpenPath(pending);
    return this.#snapshot;
  }
  /** Read string characters from `start` until the string ends or the text does; returns the next index. */
  #readString(open, text, start) {
    let runStart = start;
    for (let index = start; index < text.length; index++) {
      const code = text.charCodeAt(index);
      if (open.escape !== "") {
        if (this.#continueEscape(open, text[index])) {
          runStart = index + 1;
          continue;
        }
        runStart = index;
      }
      if (code === QUOTE) {
        if (index > runStart)
          this.#appendString(open, text.slice(runStart, index));
        this.#endString(open);
        return index + 1;
      }
      if (code === BACKSLASH) {
        if (index > runStart)
          this.#appendString(open, text.slice(runStart, index));
        open.escape = "\\";
        runStart = index + 1;
      }
    }
    if (text.length > runStart)
      this.#appendString(open, text.slice(runStart));
    return text.length;
  }
  /** Read number or literal characters from `start` up to the next delimiter; returns the delimiter's index. */
  #readScalar(text, start) {
    let end = start;
    while (end < text.length && !isDelimiter(text[end]))
      end++;
    if (end > start) {
      this.#scalar += text.slice(start, end);
      if (this.#scalarSlot !== void 0)
        this.#dirty = true;
    }
    if (end < text.length)
      this.#endScalar();
    return end;
  }
  /** Handle one character outside strings. */
  #structural(char) {
    switch (char) {
      case " ":
      case "	":
      case "\n":
      case "\r":
        return;
      case '"':
        this.#startString();
        return;
      case "{":
      case "[":
        this.#openContainer(char === "{" ? {} : []);
        return;
      case "}":
      case "]":
        this.#closeContainer();
        return;
      case ":": {
        const frame = this.#stack.at(-1);
        if (frame !== void 0 && !Array.isArray(frame.container) && frame.phase === "colon")
          frame.phase = "value";
        return;
      }
      case ",": {
        const frame = this.#stack.at(-1);
        if (frame !== void 0)
          frame.phase = Array.isArray(frame.container) ? "value" : "key";
        return;
      }
      default:
        this.#scalar = char;
        this.#scalarSlot = this.#valueSlot();
        if (this.#scalarSlot !== void 0)
          this.#dirty = true;
    }
  }
  /** Where a value starting now goes, or `undefined` when this position takes no value. */
  #valueSlot() {
    const frame = this.#stack.at(-1);
    if (frame === void 0)
      return this.#root === void 0 ? ROOT_SLOT : void 0;
    if (Array.isArray(frame.container))
      return { kind: "element", index: frame.container.length };
    if ((frame.phase === "value" || frame.phase === "colon") && frame.key !== void 0) {
      return { kind: "member", key: frame.key };
    }
    return void 0;
  }
  #startString() {
    const frame = this.#stack.at(-1);
    if (frame !== void 0 && !Array.isArray(frame.container) && (frame.phase === "key" || frame.phase === "comma")) {
      this.#string = { target: "key", slot: void 0, text: "", escape: "", path: void 0, change: void 0 };
      return;
    }
    const slot = this.#valueSlot();
    const path = this.#reportedPath(slot);
    this.#string = { target: "value", slot, text: "", escape: "", path, change: void 0 };
    this.#place(slot, "");
    if (path !== void 0)
      this.#string.change = this.#report({ kind: "set", path, value: "" });
  }
  /** The path of a value at `slot` in the innermost open container, when its changes are reported. */
  #reportedPath(slot) {
    if (slot === void 0)
      return void 0;
    if (slot.kind === "root")
      return [];
    const frame = this.#stack.at(-1);
    if (!frame.reportsInside)
      return void 0;
    return [...frame.path, slot.kind === "member" ? slot.key : slot.index];
  }
  #report(change) {
    this.#changes.push(change);
    return change;
  }
  /** Continue an escape with `char`; returns false when `char` is not part of it and must be read normally. */
  #continueEscape(open, char) {
    if (open.escape === "\\") {
      const decoded = SIMPLE_ESCAPES[char];
      if (decoded !== void 0) {
        open.escape = "";
        this.#appendString(open, decoded);
        return true;
      }
      if (char === "u") {
        open.escape = "\\u";
        return true;
      }
      open.escape = "";
      this.#appendString(open, "\\");
      return false;
    }
    if (isHexDigit(char)) {
      open.escape += char;
      if (open.escape.length < 6)
        return true;
      const decoded = String.fromCharCode(Number.parseInt(open.escape.slice(2), 16));
      open.escape = "";
      this.#appendString(open, decoded);
      return true;
    }
    const literal = open.escape;
    open.escape = "";
    this.#appendString(open, literal);
    return false;
  }
  #appendString(open, text) {
    open.text += text;
    if (open.target !== "value")
      return;
    this.#place(open.slot, open.text);
    if (open.path === void 0)
      return;
    const change = open.change;
    if (change !== void 0 && this.#changes.at(-1) === change) {
      if (change.kind === "set")
        change.value = change.value + text;
      else
        change.text += text;
      return;
    }
    open.change = this.#report({ kind: "append", path: open.path, text });
  }
  #endString(open) {
    this.#string = void 0;
    const frame = this.#stack.at(-1);
    if (open.target === "key") {
      if (frame === void 0)
        return;
      frame.key = open.text;
      frame.phase = "colon";
      return;
    }
    this.#valueEnded(frame);
  }
  #endScalar() {
    const token = this.#scalar;
    const slot = this.#scalarSlot;
    this.#scalar = "";
    this.#scalarSlot = void 0;
    const value = scalarValue(token);
    if (value !== void 0) {
      this.#place(slot, value);
      const path = this.#reportedPath(slot);
      if (path !== void 0)
        this.#report({ kind: "set", path, value });
    }
    this.#valueEnded(this.#stack.at(-1));
  }
  /** The value of the unterminated scalar at the end of the text, if it shows. */
  #pendingScalar() {
    const slot = this.#scalarSlot;
    const token = this.#scalar;
    if (slot === void 0 || token === "" || token.length > MAX_PENDING_SCALAR_LENGTH)
      return void 0;
    const value = pendingScalarValue(token);
    return value === void 0 ? void 0 : { slot, value };
  }
  #openContainer(container) {
    const slot = this.#valueSlot();
    const path = this.#reportedPath(slot);
    this.#place(slot, container);
    this.#stack.push({
      container,
      slot,
      path,
      reportsInside: path !== void 0 && path.length < MAX_REPORTED_DEPTH,
      size: 0,
      key: void 0,
      phase: Array.isArray(container) ? "value" : "key"
    });
    this.#pathCost += 1;
    this.#dirty = true;
    if (path !== void 0)
      this.#report({ kind: "set", path, value: Array.isArray(container) ? [] : {} });
  }
  #closeContainer() {
    const frame = this.#stack.pop();
    if (frame === void 0)
      return;
    this.#pathCost -= frame.size + 1;
    this.#dirty = true;
    if (frame.path !== void 0 && !frame.reportsInside) {
      this.#report({ kind: "set", path: frame.path, value: frame.container });
    }
    this.#valueEnded(this.#stack.at(-1));
  }
  /** A value in `frame`, or the root value when there is no frame, has ended. */
  #valueEnded(frame) {
    if (frame === void 0) {
      if (this.#root !== void 0)
        this.#done = true;
      return;
    }
    frame.phase = "comma";
  }
  /** Set the value at `slot` in the open path; nothing for a value in an invalid position. */
  #place(slot, value) {
    if (slot === void 0)
      return;
    this.#dirty = true;
    if (slot.kind === "root") {
      this.#root = value;
      return;
    }
    const frame = this.#stack.at(-1);
    if (slot.kind === "element") {
      const elements = frame.container;
      if (slot.index === elements.length) {
        frame.size++;
        this.#pathCost++;
      }
      elements[slot.index] = value;
      return;
    }
    const members = frame.container;
    if (!Object.hasOwn(members, slot.key)) {
      frame.size++;
      this.#pathCost++;
    }
    setMember(members, slot.key, value);
  }
  /** A fresh snapshot: copies of the open containers, innermost first, each holding the copy of its open child. */
  #copyOpenPath(pending) {
    if (this.#stack.length === 0)
      return pending?.slot.kind === "root" ? pending.value : this.#root;
    let child;
    let childSlot;
    for (let depth = this.#stack.length - 1; depth >= 0; depth--) {
      const frame = this.#stack[depth];
      const copy = Array.isArray(frame.container) ? frame.container.slice() : { ...frame.container };
      if (depth === this.#stack.length - 1 && pending !== void 0)
        setSlot(copy, pending.slot, pending.value);
      if (child !== void 0 && childSlot !== void 0)
        setSlot(copy, childSlot, child);
      child = copy;
      childSlot = frame.slot;
    }
    return childSlot?.kind === "root" ? child : this.#root;
  }
};
var ToolCallArgumentsStream = class {
  static {
    __name(this, "ToolCallArgumentsStream");
  }
  #text = "";
  #parser = new StreamingJsonParser();
  #snapshot = void 0;
  #arguments = {};
  /** The raw argument text received so far. */
  get text() {
    return this.#text;
  }
  /** Append a piece of argument text; returns the partial arguments, a new object whenever they changed. */
  append(delta) {
    this.#text += delta;
    this.#parser.append(delta);
    return this.#partial();
  }
  /**
   * Take the provider's complete argument text. When it continues the text so far, returns the missing piece as
   * `delta`; otherwise starts over with `text`. Either way returns the partial arguments of `text`.
   */
  replace(text) {
    if (text.startsWith(this.#text)) {
      const delta = text.slice(this.#text.length);
      return { delta, arguments: delta === "" ? this.#partial() : this.append(delta) };
    }
    this.#text = "";
    this.#parser = new StreamingJsonParser();
    return { delta: void 0, arguments: this.append(text) };
  }
  /** The final arguments: one full parse of the complete text, which reports malformed arguments. */
  finish() {
    return parseToolCallArguments(this.#text);
  }
  #partial() {
    const snapshot = this.#parser.snapshot();
    if (snapshot === this.#snapshot)
      return this.#arguments;
    this.#snapshot = snapshot;
    this.#arguments = typeof snapshot === "object" && snapshot !== null && !Array.isArray(snapshot) ? snapshot : {};
    return this.#arguments;
  }
};
var SIMPLE_ESCAPES = {
  '"': '"',
  "\\": "\\",
  "/": "/",
  b: "\b",
  f: "\f",
  n: "\n",
  r: "\r",
  t: "	"
};
function isDelimiter(char) {
  return char === " " || char === "	" || char === "\n" || char === "\r" || char === "," || char === ":" || char === "{" || char === "}" || char === "[" || char === "]" || char === '"';
}
__name(isDelimiter, "isDelimiter");
function isHexDigit(char) {
  return char >= "0" && char <= "9" || char >= "a" && char <= "f" || char >= "A" && char <= "F";
}
__name(isHexDigit, "isHexDigit");
function scalarValue(token) {
  for (const [literal, value] of LITERALS)
    if (token === literal)
      return value;
  return NUMBER.test(token) ? Number(token) : void 0;
}
__name(scalarValue, "scalarValue");
function pendingScalarValue(token) {
  for (const [literal, value] of LITERALS)
    if (literal.startsWith(token))
      return value;
  if (NUMBER.test(token))
    return Number(token);
  const mantissa = NUMBER_WITHOUT_EXPONENT_DIGITS.exec(token);
  return mantissa === null ? void 0 : Number(mantissa[1]);
}
__name(pendingScalarValue, "pendingScalarValue");
function setSlot(container, slot, value) {
  if (slot.kind === "element")
    container[slot.index] = value;
  else if (slot.kind === "member")
    setMember(container, slot.key, value);
}
__name(setSlot, "setSlot");
function setMember(members, key, value) {
  if (key === "__proto__") {
    Object.defineProperty(members, key, { value, writable: true, enumerable: true, configurable: true });
  } else {
    members[key] = value;
  }
}
__name(setMember, "setMember");

export {
  repairJson,
  parseJsonWithRepair,
  parseToolCallArguments,
  parseStreamingJson,
  StreamingJsonParser,
  ToolCallArgumentsStream
};
