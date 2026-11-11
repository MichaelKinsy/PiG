// The JSON form of values on the control channel, for what control JSON cannot say. A `bytes()` leaf (a Uint8Array here, a []byte
// in Go) is `{ "$bytes": "<base64>" }`. So that no plain datum can be mistaken for it, every object key that starts with `$` gets
// one more `$` when a value is encoded and loses it when decoded: a plain `{ "$bytes": x }` crosses as `{ "$$bytes": x }`, and an
// object whose only key is `$bytes` (a string) is always bytes. plugins_node_wire.go is the Go side.

/** `value` as control JSON. */
export function encodeValue(value) {
	if (value instanceof Uint8Array) return { $bytes: Buffer.from(value.buffer, value.byteOffset, value.byteLength).toString("base64") };
	if (Array.isArray(value)) return value.map(encodeValue);
	if (typeof value === "object" && value !== null) {
		const out = {};
		for (const [key, member] of Object.entries(value)) out[key.startsWith("$") ? `$${key}` : key] = encodeValue(member);
		return out;
	}
	return value;
}

/** The value of control JSON `json`: `encodeValue`'s inverse. */
export function decodeValue(json) {
	if (Array.isArray(json)) return json.map(decodeValue);
	if (typeof json === "object" && json !== null) {
		const keys = Object.keys(json);
		if (keys.length === 1 && keys[0] === "$bytes" && typeof json.$bytes === "string") return new Uint8Array(Buffer.from(json.$bytes, "base64"));
		const out = {};
		for (const [key, member] of Object.entries(json)) out[key.startsWith("$$") ? key.slice(1) : key] = decodeValue(member);
		return out;
	}
	return json;
}
