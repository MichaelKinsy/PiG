// pi-ai's `Type` (TypeBox 1.3.27): what each constructor and guard of the first slice returns for the arguments the extensions in the npm `pi-package` scan pass, plus the
// edges: option bags (members added after the constructor's own, a member replaced in place), key order (array-index keys first), Optional/Readonly/Immutable/Unsafe markers
// (reported as hidden members: JSON drops them), Record key kinds, and Enum from a list and from a TypeScript-enum-like object.
//
// An argument is JSON, except {"$call": "<member of Type>", "args": [...]}, which is the value that call returns (arguments are resolved first). A case's result is what
// the answer is: a guard's boolean, or for a schema {"json": JSON.stringify(schema), "hidden": {<non-enumerable own members>}}, so the key order, the numbers' text and the hidden
// markers are all compared.
// {"$fn": "<name>", ...} is a predicate or message function (see predicateFor below), for Type.Refine; each SDK's test builds the same function from the same spec.
const t = (name, ...args) => ({ $call: name, args });
const fn = (name, spec = {}) => ({ $fn: name, ...spec });
const S = t("String"), N = t("Number"), I = t("Integer"), B = t("Boolean");
const opt = (x) => t("Optional", x);
const cases = [];
const add = (name, node) => cases.push({ name, args: [node] });

for (const [name, member] of Object.entries({ any: "Any", unknown: "Unknown", string: "String", number: "Number", integer: "Integer", boolean: "Boolean", null: "Null", never: "Never", undefined: "Undefined", void: "Void", symbol: "Symbol", bigint: "BigInt" })) {
  add(`${name}`, t(member));
  add(`${name} with options`, t(member, { description: "d", title: "T", default: 1 }));
}
add("string options after the type", t("String", { minLength: 1, maxLength: 5, pattern: "^a", format: "email" }));
add("string option replaces type in place", t("String", { type: "x", description: "d" }));
add("number bounds", t("Number", { minimum: 0, maximum: 10, multipleOf: 0.5, exclusiveMinimum: -1 }));
add("integer bounds", t("Integer", { minimum: 1 }));
add("options with array-index keys", t("String", { b: 1, 2: "two", a: 3, 1: "one", 10: "ten", "01": "zero-one" }));
add("ref", t("Ref", "#/$defs/A"));
add("ref with options", t("Ref", "A", { description: "d" }));
add("array of string", t("Array", S));
add("array with options", t("Array", I, { minItems: 1, maxItems: 3, uniqueItems: true }));
add("array of objects", t("Array", t("Object", { a: S })));
add("union of string and number", t("Union", [S, N]));
add("union with null", t("Union", [S, t("Null")], { description: "maybe" }));
add("union of literals", t("Union", [t("Literal", "a"), t("Literal", "b"), t("Literal", 3)]));
add("empty union", t("Union", []));
add("intersect", t("Intersect", [t("Object", { a: S }), t("Object", { b: N })]));
add("intersect with options", t("Intersect", [t("Object", { a: S })], { description: "d" }));
add("literal string", t("Literal", "x"));
add("literal number", t("Literal", 1.5));
add("literal zero", t("Literal", 0));
add("literal negative", t("Literal", -3));
add("literal true", t("Literal", true));
add("literal false", t("Literal", false));
add("literal with options", t("Literal", "x", { description: "d" }));
add("literal of an object throws", t("Literal", { a: 1 }));
add("literal of null throws", t("Literal", null));
add("literal of an array throws", t("Literal", [1]));
add("enum from a list of strings", t("Enum", ["a", "b", "c"]));
add("enum from a list of mixed values", t("Enum", ["a", 1, 2.5]));
add("enum from an empty list", t("Enum", []));
add("enum from an object", t("Enum", { A: "a", B: "b" }));
add("enum from a numeric TypeScript enum object", t("Enum", { 0: "A", 1: "B", A: 0, B: 1 }));
add("enum from a mixed enum object", t("Enum", { X: 1, Y: "y", 5: "X" }));
add("enum with options", t("Enum", ["a"], { description: "d" }));
add("enum object whose key is numeric-looking text", t("Enum", { "1e3": "n", " ": "space", "": "empty", K: "k", "0x10": "hex", Infinity: "inf", "-5": "neg", ".5": "dot", "5.": "dot2", "1_0": "underscore" }));
add("object empty", t("Object", {}));
add("object with required and optional", t("Object", { a: S, b: opt(N), c: I }));
add("object all optional", t("Object", { a: opt(S), b: opt(N) }));
add("object with options", t("Object", { a: S }, { additionalProperties: false, description: "d" }));
add("object option replaces a member in place", t("Object", { a: S }, { type: "x", required: ["z"], extra: 1 }));
add("object with array-index property keys", t("Object", { b: S, 2: N, a: S, 1: B, 10: I }));
add("object nested", t("Object", { a: t("Object", { b: t("Array", t("Object", { c: opt(S) })) }) }));
add("object with a readonly property", t("Object", { a: t("Readonly", S), b: opt(t("Readonly", N)) }));
add("object with optional of unknown", t("Object", { a: opt(t("Unknown")), b: t("Any") }));
add("object with an Unsafe property", t("Object", { a: t("Unsafe", { type: "string", enum: ["x"] }) }));
add("optional", opt(S));
add("optional twice", opt(opt(S)));
add("optional of object", opt(t("Object", { a: S })));
add("readonly", t("Readonly", S));
add("immutable", t("Immutable", S));
add("readonly and optional", t("Readonly", opt(S)));
add("optional readonly immutable", t("Immutable", t("Readonly", opt(S))));
add("optional keeps the options", opt(t("String", { description: "d" })));
add("unsafe of a plain schema", t("Unsafe", { type: "string", enum: ["a", "b"] }));
add("unsafe of a schema", t("Unsafe", S));
add("unsafe keeps the kind and adds the marker", t("Unsafe", t("Number", { minimum: 0 })));
add("unsafe with an empty object", t("Unsafe", {}));
add("with adds options", t("With", S, { description: "d" }));
add("with replaces an option", t("With", t("String", { description: "old", minLength: 1 }), { description: "new" }));
add("with keeps the markers", t("With", opt(S), { description: "d" }));
add("with of a ref is deferred", t("With", t("Ref", "A"), { description: "d" }));
add("record of string to number", t("Record", S, N));
add("record of number to string", t("Record", N, S));
add("record of integer to boolean", t("Record", I, B));
add("record of string with a pattern", t("Record", t("String", { pattern: "^x" }), N));
add("record of string with options", t("Record", S, N, { description: "d" }));
add("record of any", t("Record", t("Any"), S));
add("record of boolean keys", t("Record", B, N));
add("record of a string literal", t("Record", t("Literal", "a"), N));
add("record of a number literal", t("Record", t("Literal", 1.5), N));
add("record of an integer-valued literal", t("Record", t("Literal", 7), N));
add("record of true", t("Record", t("Literal", true), N));
add("record of false", t("Record", t("Literal", false), N));
add("record of a union of literals", t("Record", t("Union", [t("Literal", "a"), t("Literal", "b")]), S));
add("record of a union with an array-index literal", t("Record", t("Union", [t("Literal", "b"), t("Literal", 2), t("Literal", "a"), t("Literal", 1)]), S));
add("record of a union containing string", t("Record", t("Union", [t("Literal", "a"), S]), S));
add("record of a nested union", t("Record", t("Union", [t("Union", [t("Literal", "a")]), t("Literal", "b")]), S));
add("record of a union with a duplicate literal", t("Record", t("Union", [t("Literal", "a"), t("Literal", "b"), t("Literal", "a")]), S));
add("record of a union with a non-literal", t("Record", t("Union", [t("Literal", "a"), B]), S));
add("record of null key is an empty object", t("Record", t("Null"), S));
add("record of a ref key is deferred", t("Record", t("Ref", "K"), S));
add("record of a ref key with options is deferred", t("Record", t("Ref", "K"), S, { description: "d" }));
add("record of optional value", t("Record", S, opt(N)));
add("record value that is an object", t("Record", S, t("Object", { a: S })));
// An array as a "schema" (Pi's Memory.Update clones it and adds the hidden member), numbers JavaScript writes in other ways than a plain print, and strings with the characters
// JSON.stringify escapes or leaves raw.
add("unsafe of an array", t("Unsafe", [1, "a", null]));
add("unsafe of an empty array", t("Unsafe", []));
add("unsafe of nested arrays", t("Unsafe", [[1], { a: 2 }]));
add("optional of an array", opt([1, 2]));
add("readonly of an array", t("Readonly", ["x"]));
add("with options on an array", t("With", [1], { description: "d" }));
add("IsUnsafe of an unsafe array", t("IsUnsafe", t("Unsafe", [1])));
add("IsSchema of an unsafe array", t("IsSchema", t("Unsafe", [1])));
add("IsOptional of an optional array", t("IsOptional", opt([1])));
const numbers = [1e21, 1e-7, 1e-6, 1.5e-10, 123456789012345680000, 12345678901234567890, 5e-324, 1.7976931348623157e308, 0.1 + 0.2, 4.35, 2 ** 53, 2 ** 53 + 2, 100, 0.000001234, 1e300, -1e21, -1e-7, 1.5, 0, 123456789, 0.5, 1e20, 999999999999999900000];
for (const n of numbers) {
  add(`number option ${n}`, t("Number", { minimum: n, maximum: n }));
  add(`literal ${n}`, t("Literal", n));
  add(`record of literal ${n}`, t("Record", t("Literal", n), S));
}
add("numbers in an enum list", t("Enum", [1e21, 1e-7, 0.1, 100]));
add("integer option array of numbers", t("Array", I, { examples: [1e21, 1e-7, 0.5, 1] }));
add("string with HTML characters", t("String", { description: "a<b>&c" }));
add("string with line separators", t("String", { description: "a\u2028b\u2029c" }));
add("string with control characters", t("String", { description: "a\u0007b\u001fc\td\ne\rf\bg\fh" }));
add("string with quotes and backslash", t("String", { description: 'q"b\\s/' }));
add("string with unicode", t("String", { description: "héllo wörld \u4e2d\u6587 \u{1F600}" }));
add("string with DEL", t("String", { description: "a\u007fb" }));
add("property names that need escapes", t("Object", { 'a"b': S, "c\\d": N, "e\nf": B, "<>": S }));

// The tool parameters the conformance fixtures register in every SDK (TestConformance_TypeSchemaToolParametersAgreeAcrossSDKs): what the host holds must equal Pi's.
// Refine: the hidden ~refine list (the vector records how many entries it has; a function has no JSON form) and the guard.
add("refine of a string", t("Refine", S, fn("always")));
add("refine twice", t("Refine", t("Refine", S, fn("always")), fn("never")));
add("refine with an error function", t("Refine", S, fn("never"), fn("text", { text: "no" })));
add("refine keeps the other markers", t("Refine", opt(S), fn("always")));
add("optional keeps the refinement", opt(t("Refine", S, fn("always"))));
add("refine of an object", t("Refine", t("Object", { a: S }), fn("always")));
add("refine of an array keeps ~refine and length", t("Refine", [1, 2], fn("always")));
add("IsRefine of a refined schema", t("IsRefine", t("Refine", S, fn("always"))));
add("IsRefine of a plain schema", t("IsRefine", S));
add("IsRefine of an optional refined schema", t("IsRefine", opt(t("Refine", S, fn("always")))));
add("IsRefine of null", t("IsRefine", null));
add("refine does not change the JSON of a union", t("Refine", t("Union", [S, N]), fn("always")));
add("conformance tool parameters", t("Object", {
  query: t("String", { description: "What to look up" }),
  limit: opt(t("Integer", { minimum: 1, maximum: 50 })),
  mode: t("Union", [t("Literal", "fast"), t("Literal", "deep")]),
  tags: opt(t("Array", S)),
  filter: opt(t("Record", S, N)),
}, { additionalProperties: false }));
for (const [name, args] of Object.entries({
  IsAny: [t("Any")], IsArray: [t("Array", S)], IsBigInt: [t("BigInt")], IsBoolean: [B], IsEnum: [t("Enum", ["a"])], IsInteger: [I], IsIntersect: [t("Intersect", [S])],
  IsLiteral: [t("Literal", "a")], IsNever: [t("Never")], IsNull: [t("Null")], IsNumber: [N], IsObject: [t("Object", {})], IsRecord: [t("Record", S, N)], IsRef: [t("Ref", "A")],
  IsString: [S], IsSymbol: [t("Symbol")], IsUndefined: [t("Undefined")], IsUnion: [t("Union", [S])], IsUnknown: [t("Unknown")], IsVoid: [t("Void")],
})) {
  add(`${name} of its own kind`, t(name, ...args));
  add(`${name} of a string schema`, t(name, name === "IsString" ? N : S));
  add(`${name} of the JSON schema`, t(name, { type: "x" }));
}
add("IsString of a plain JSON string schema", t("IsString", { type: "string" }));
add("IsString of a plain object with ~kind", t("IsString", { "~kind": "String" }));
add("IsString of null", t("IsString", null));
add("IsString of a string", t("IsString", "x"));
add("IsString of a number", t("IsString", 1));
add("IsString of an array", t("IsString", []));
add("IsString of an optional string", t("IsString", opt(S)));
add("IsObject of an Unsafe object", t("IsObject", t("Unsafe", t("Object", {}))));
add("IsKind of a matching kind", t("IsKind", S, "String"));
add("IsKind of another kind", t("IsKind", S, "Number"));
add("IsKind of a plain object", t("IsKind", { "~kind": "String" }, "String"));
add("IsKind of null", t("IsKind", null, "String"));
add("IsSchema of an object", t("IsSchema", {}));
add("IsSchema of a schema", t("IsSchema", S));
add("IsSchema of an array", t("IsSchema", []));
add("IsSchema of null", t("IsSchema", null));
add("IsSchema of a string", t("IsSchema", "x"));
add("IsSchema of a number", t("IsSchema", 1));
add("IsSchema of a boolean", t("IsSchema", true));
add("IsOptional of an optional", t("IsOptional", opt(S)));
add("IsOptional of a required", t("IsOptional", S));
add("IsOptional of a plain object with ~optional", t("IsOptional", { "~optional": true }));
add("IsOptional of null", t("IsOptional", null));
add("IsReadonly of a readonly", t("IsReadonly", t("Readonly", S)));
add("IsReadonly of a plain", t("IsReadonly", S));
add("IsImmutable of an immutable", t("IsImmutable", t("Immutable", S)));
add("IsImmutable of a plain", t("IsImmutable", S));
add("IsUnsafe of an unsafe", t("IsUnsafe", t("Unsafe", { type: "string" })));
add("IsUnsafe of a plain", t("IsUnsafe", S));
add("IsUnsafe of a plain object with ~unsafe null", t("IsUnsafe", { "~unsafe": null }));
add("IsUnsafe of a plain object with ~unsafe true", t("IsUnsafe", { "~unsafe": true }));
add("IsUnsafe of an array", t("IsUnsafe", []));
add("IsEnumValue of a string", t("IsEnumValue", "a"));
add("IsEnumValue of a number", t("IsEnumValue", 1.5));
add("IsEnumValue of a boolean", t("IsEnumValue", true));
add("IsEnumValue of null", t("IsEnumValue", null));
add("IsEnumValue of an object", t("IsEnumValue", {}));
export { cases };

// The predicate and message functions a case names. A spec's "tag" labels the call in the log of validateToolArguments vectors (the same function there); here only the shape matters.
export const calls = [];
export const specOf = new WeakMap();
export function predicateFor(spec) {
  const fn = predicateFunction(spec);
  specOf.set(fn, spec);
  return fn;
}
function predicateFunction(spec) {
  const log = (kind) => calls.push(`${kind}:${spec.tag ?? spec.$fn}`);
  switch (spec.$fn) {
    case "always": return () => { log("check"); return true; };
    case "never": return () => { log("check"); return false; };
    case "lengthAtLeast": return (v) => { log("check"); return String(v).length >= spec.n; };
    case "equals": return (v) => { log("check"); return v === spec.value; };
    case "even": return (v) => { log("check"); return v % 2 === 0; };
    case "greaterThan": return (v) => { log("check"); return v > spec.n; };
    case "throws": return () => { log("check"); throw new Error(spec.message); };
    case "throwsText": return () => { log("check"); throw spec.message; };
    case "text": return () => { log("error"); return spec.text; };
    case "textWithValue": return (v) => { log("error"); return spec.prefix + String(v); };
    case "errorThrows": return () => { log("error"); throw new Error(spec.message); };
    default: throw new Error(`no predicate ${spec.$fn}`);
  }
}

// Resolve {"$call"} nodes, then report the answer. JSON.parse keeps the order of the keys a JavaScript object has (array indexes first), which is what Pi's callers pass.
export const resolve = (Type, node) => {
  if (Array.isArray(node)) return node.map((x) => resolve(Type, x));
  if (node && typeof node === "object") {
    if (typeof node.$call === "string") {
      const fn = Type[node.$call];
      if (typeof fn !== "function") throw new Error(`Type has no ${node.$call}`);
      return fn(...node.args.map((x) => resolve(Type, x)));
    }
    if (typeof node.$fn === "string") return predicateFor(node);
    return Object.fromEntries(Object.entries(node).map(([k, v]) => [k, resolve(Type, v)]));
  }
  return node;
};
export const call = (Type, node) => {
  const value = resolve(Type, node);
  if (typeof value !== "object" || value === null) return value;
  const hidden = {};
  for (const key of Reflect.ownKeys(value)) {
    if (Object.keys(value).includes(key)) continue;
    // ~refine holds functions, which JSON cannot carry: the vector records how many there are.
    hidden[String(key)] = key === "~refine" ? { count: value[key].length } : value[key];
  }
  return { json: JSON.stringify(value), hidden };
};
