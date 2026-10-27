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

// ---- the type engine (slices 2-5): Pick, Omit, Partial, Required, KeyOf, Index, Mapped, Conditional, Extends, Evaluate, Instantiate, Cyclic, Module, Script, ... ----
// Every case below is served by the host's engine (internal/typebox/engine), a port of TypeBox's build/type/{action,engine,extends,script}.
const L = (v) => t("Literal", v), O = (props, o) => (o === undefined ? t("Object", props) : t("Object", props, o)), U = (...a) => t("Union", a), Arr = (x) => t("Array", x);
const P = O({ a: S, b: opt(N), c: t("Readonly", B), 1: I });
const Q = O({ x: L("x"), y: opt(Arr(S)) });
add("pick one key", t("Pick", P, ["a"]));
add("pick keeps optional and readonly markers", t("Pick", P, ["b", "c"]));
add("pick with an array-index key", t("Pick", P, ["1", "a"]));
add("pick no keys", t("Pick", P, []));
add("pick a missing key", t("Pick", P, ["nope"]));
add("pick with an indexer union", t("Pick", P, U(L("a"), L("c"))));
add("pick with options", t("Pick", P, ["a"], { description: "d" }));
add("pick of an intersect", t("Pick", t("Intersect", [P, Q]), ["a", "x"]));
add("pick of a union", t("Pick", U(O({ a: S, b: N }), O({ a: N, c: B })), ["a"]));
add("pick of a tuple", t("Pick", t("Tuple", [S, N]), ["0"]));
add("pick of a ref is deferred", t("Pick", t("Ref", "A"), ["a"]));
add("pick of a number literal key", t("Pick", P, [1]));
add("pick keys that are not literal values are ignored", t("Pick", P, ["a", { x: 1 }, null]));
add("pick of a string throws", t("Pick", S, ["a"]));
add("omit one key", t("Omit", P, ["a"]));
add("omit several keys", t("Omit", P, ["a", "b", "1"]));
add("omit all keys", t("Omit", P, ["a", "b", "c", "1"]));
add("omit a missing key", t("Omit", P, ["zzz"]));
add("omit with an indexer", t("Omit", P, L("a")));
add("omit with options", t("Omit", P, ["a"], { additionalProperties: false }));
add("omit of a union", t("Omit", U(O({ a: S, b: N }), O({ a: N, c: B })), ["a"]));
add("omit of a ref is deferred", t("Omit", t("Ref", "A"), ["a"]));
add("partial of an object", t("Partial", P));
add("partial keeps the options", t("Partial", O({ a: S }, { additionalProperties: false })));
add("partial with options", t("Partial", P, { description: "d" }));
add("partial of an intersect", t("Partial", t("Intersect", [O({ a: S }), O({ b: N })])));
add("partial of a union", t("Partial", U(O({ a: S }), O({ b: N }))));
add("partial of a string is an empty object", t("Partial", S));
add("partial of a ref is deferred", t("Partial", t("Ref", "A")));
add("partial of a cyclic", t("Partial", t("Cyclic", { A: O({ a: t("Ref", "A") }) }, "A")));
add("required of an object", t("Required", O({ a: opt(S), b: opt(N), c: B })));
add("required keeps readonly", t("Required", O({ a: t("Readonly", opt(S)) })));
add("required of a union", t("Required", U(O({ a: opt(S) }), O({ b: opt(N) }))));
add("required of a number is an empty object", t("Required", N));
add("required of a ref is deferred", t("Required", t("Ref", "A")));
add("readonly object", t("ReadonlyObject", P));
add("readonly type of an array", t("ReadonlyType", Arr(S)));
add("readonly object of a tuple", t("ReadonlyObject", t("Tuple", [S])));
add("readonly object of a union", t("ReadonlyObject", U(O({ a: S }), Arr(N))));
add("readonly object of a string", t("ReadonlyObject", S));
add("keyof an object", t("KeyOf", P));
add("keyof an empty object", t("KeyOf", O({})));
add("keyof a record of strings", t("KeyOf", t("Record", S, N)));
add("keyof a record of integers", t("KeyOf", t("Record", I, N)));
add("keyof an array", t("KeyOf", Arr(S)));
add("keyof a tuple", t("KeyOf", t("Tuple", [S, N, B])));
add("keyof any", t("KeyOf", t("Any")));
add("keyof a string is never", t("KeyOf", S));
add("keyof a union keeps the shared keys", t("KeyOf", U(O({ a: S, b: N }), O({ a: N, c: B }))));
add("keyof an intersect", t("KeyOf", t("Intersect", [O({ a: S }), O({ b: N })])));
add("keyof with options", t("KeyOf", P, { description: "d" }));
add("keyof a ref is deferred", t("KeyOf", t("Ref", "A")));
add("index an object by a literal", t("Index", P, L("a")));
add("index an object by a union", t("Index", P, U(L("a"), L("b"))));
add("index an object by keys", t("Index", P, ["a", "c"]));
add("index an object by number", t("Index", O({ 0: S, 1: N, a: B }), N));
add("index a missing key is never", t("Index", P, L("zz")));
add("index an array by number", t("Index", Arr(S), N));
add("index an array by length", t("Index", Arr(S), L("length")));
add("index an array by a string is never", t("Index", Arr(S), S));
add("index a tuple by a literal", t("Index", t("Tuple", [S, N, B]), L(1)));
add("index a tuple by a union", t("Index", t("Tuple", [S, N, B]), U(L(0), L(2))));
add("index a tuple by length", t("Index", t("Tuple", [S, N]), L("length")));
add("index a tuple by number", t("Index", t("Tuple", [S, N]), N));
add("index an intersect", t("Index", t("Intersect", [O({ a: S }), O({ b: N })]), L("b")));
add("index of a ref is deferred", t("Index", t("Ref", "A"), L("a")));
add("index with options", t("Index", P, L("a"), { description: "d" }));
add("exclude from a union", t("Exclude", U(S, N, B), S));
add("exclude with a union", t("Exclude", U(S, N, B), U(S, N)));
add("exclude literals", t("Exclude", U(L("a"), L("b"), L("c")), L("b")));
add("exclude everything", t("Exclude", S, S));
add("exclude nothing", t("Exclude", S, N));
add("extract from a union", t("Extract", U(S, N, B), U(S, N)));
add("extract literals", t("Extract", U(L("a"), L("b"), S), S));
add("extract nothing is never", t("Extract", S, N));
add("non nullable", t("NonNullable", U(S, t("Null"), t("Undefined"))));
add("non nullable of a string", t("NonNullable", S));
add("capitalize a literal", t("Capitalize", L("hello")));
add("uncapitalize a literal", t("Uncapitalize", L("Hello")));
add("uppercase a literal", t("Uppercase", L("hello")));
add("lowercase a literal", t("Lowercase", L("HELLO")));
add("uppercase a union", t("Uppercase", U(L("a"), L("b"))));
add("uppercase ß", t("Uppercase", L("stra\u00dfe")));
add("lowercase İ", t("Lowercase", L("\u0130")));
add("lowercase a final sigma", t("Lowercase", L("\u03a3\u0391\u03a3")));
add("capitalize an empty literal throws", t("Capitalize", L("")));
add("uncapitalize an empty literal throws", t("Uncapitalize", L("")));
add("capitalize an astral first letter", t("Capitalize", L("\u{1F600}a")));
add("uppercase a number literal is unchanged", t("Uppercase", L(5)));
add("uppercase a string is unchanged", t("Uppercase", S));
add("uppercase a template literal", t("Uppercase", t("TemplateLiteral", [L("a"), U(L("b"), L("c"))])));
add("uppercase a ref is deferred", t("Uppercase", t("Ref", "A")));
add("parameters of a function", t("Parameters", t("Function", [S, opt(N)], B)));
add("parameters of a string is an empty tuple", t("Parameters", S));
add("constructor parameters", t("ConstructorParameters", t("Constructor", [S, N], O({ a: S }))));
add("return type of a function", t("ReturnType", t("Function", [S], N)));
add("return type of a string is never", t("ReturnType", S));
add("instance type of a constructor", t("InstanceType", t("Constructor", [S], O({ a: S }))));
add("instance type of a string is never", t("InstanceType", S));
add("function", t("Function", [S, N], B));
add("function with options", t("Function", [], t("Void"), { description: "d" }));
add("constructor", t("Constructor", [S], O({})));
add("tuple", t("Tuple", [S, N, L(1)]));
add("empty tuple", t("Tuple", []));
add("tuple with options", t("Tuple", [S], { description: "d" }));
add("rest", t("Rest", t("Tuple", [S])));
add("this", t("This"));
add("this with options", t("This", { description: "d" }));
add("identifier", t("Identifier", "K"));
add("infer", t("Infer", "U"));
add("infer with a constraint", t("Infer", "U", S));
add("parameter", t("Parameter", "T"));
add("parameter with a constraint", t("Parameter", "T", S));
add("parameter with a default", t("Parameter", "T", S, L("a")));
add("generic", t("Generic", [t("Parameter", "T")], Arr(t("Ref", "T"))));
add("dependent", t("Dependent", S, N, B));
add("dependent with options", t("Dependent", S, N, B, { description: "d" }));
add("cyclic", t("Cyclic", { A: O({ next: opt(t("Ref", "A")) }) }, "A"));
add("cyclic with several definitions", t("Cyclic", { A: O({ b: t("Ref", "B") }), B: O({ a: opt(t("Ref", "A")) }) }, "B", { description: "d" }));
add("template literal from a string", t("TemplateLiteral", "a${string}b"));
add("template literal from a union", t("TemplateLiteral", "${'a'|'b'}-${1|2}"));
add("template literal from types", t("TemplateLiteral", [L("a"), S]));
add("template literal of numbers", t("TemplateLiteral", [N, L("-"), I]));
add("template literal of a boolean", t("TemplateLiteral", [B]));
add("template literal of a bigint", t("TemplateLiteral", [t("BigInt")]));
add("template literal with options", t("TemplateLiteral", "a${string}", { description: "d" }));
add("template literal of nested templates", t("TemplateLiteral", [t("TemplateLiteral", "a${string}"), L("b")]));
add("template literal of an enum", t("TemplateLiteral", [t("Enum", ["x", "y"])]));
add("template literal of a never", t("TemplateLiteral", [t("Never")]));
add("template literal with an unterminated interpolation throws", t("TemplateLiteral", "a${string"));
add("record key of a record", t("RecordKey", t("Record", S, N)));
add("record key of a number record", t("RecordKey", t("Record", N, N)));
add("record key of an integer record", t("RecordKey", t("Record", I, N)));
add("record pattern", t("RecordPattern", t("Record", S, N)));
add("record value", t("RecordValue", t("Record", S, N)));
add("record of an enum key", t("Record", t("Enum", ["a", "b"]), N));
add("record of an intersect key", t("Record", t("Intersect", [U(L("a"), L("b")), U(L("b"), L("c"))]), N));
add("record of a finite template literal key", t("Record", t("TemplateLiteral", "a${'x'|'y'}"), N));
add("record of an infinite template literal key", t("Record", t("TemplateLiteral", "a${string}"), N));
add("evaluate a union of unions", t("Evaluate", U(U(S, N), B)));
add("evaluate an intersect of objects", t("Evaluate", t("Intersect", [O({ a: S }), O({ b: N })])));
add("evaluate an intersect of overlapping objects", t("Evaluate", t("Intersect", [O({ a: S, b: opt(N) }), O({ a: S, b: opt(N), c: B })])));
add("evaluate an intersect of disjoint primitives", t("Evaluate", t("Intersect", [S, N])));
add("evaluate an intersect of a union and a type", t("Evaluate", t("Intersect", [U(S, N), S])));
add("evaluate an intersect of unknown and a type", t("Evaluate", t("Intersect", [t("Unknown"), S])));
add("evaluate an intersect of any", t("Evaluate", t("Intersect", [t("Any"), S])));
add("evaluate a broader union", t("Evaluate", U(L("a"), S)));
add("evaluate a union with never", t("Evaluate", U(S, t("Never"))));
add("evaluate an enum", t("Evaluate", t("Enum", ["a", "b"])));
add("evaluate a union with unknown", t("Evaluate", U(S, t("Unknown"))));
add("evaluate with options", t("Evaluate", U(S, N), { description: "d" }));
add("evaluate an intersect of tuples", t("Evaluate", t("Intersect", [t("Tuple", [S, N]), t("Tuple", [S, N])])));
add("evaluate an intersect with readonly properties", t("Evaluate", t("Intersect", [O({ a: t("Readonly", S) }), O({ a: t("Readonly", S) })])));
add("extends string string", t("Extends", {}, S, S));
add("extends string number", t("Extends", {}, S, N));
add("extends literal string", t("Extends", {}, L("a"), S));
add("extends string literal", t("Extends", {}, S, L("a")));
add("extends any string", t("Extends", {}, t("Any"), S));
add("extends string unknown", t("Extends", {}, S, t("Unknown")));
add("extends never string", t("Extends", {}, t("Never"), S));
add("extends integer number", t("Extends", {}, I, N));
add("extends union string", t("Extends", {}, U(S, N), S));
add("extends string union", t("Extends", {}, S, U(S, N)));
add("extends object object", t("Extends", {}, O({ a: S, b: N }), O({ a: S })));
add("extends object missing property", t("Extends", {}, O({ a: S }), O({ b: S })));
add("extends optional property", t("Extends", {}, O({ a: opt(S) }), O({ a: S })));
add("extends object optional right", t("Extends", {}, O({}), O({ a: opt(S) })));
add("extends tuple tuple", t("Extends", {}, t("Tuple", [S, N]), t("Tuple", [S, N])));
add("extends tuple array", t("Extends", {}, t("Tuple", [S, S]), Arr(S)));
add("extends array array", t("Extends", {}, Arr(S), Arr(S)));
add("extends immutable array", t("Extends", {}, t("Immutable", Arr(S)), Arr(S)));
add("extends function function", t("Extends", {}, t("Function", [S], N), t("Function", [S], N)));
add("extends function with a void return", t("Extends", {}, t("Function", [], N), t("Function", [], t("Void"))));
add("extends constructor", t("Extends", {}, t("Constructor", [], O({})), t("Constructor", [], O({}))));
add("extends record", t("Extends", {}, t("Record", S, N), t("Record", S, N)));
add("extends object record", t("Extends", {}, O({ a: N }), t("Record", S, N)));
add("extends record object", t("Extends", {}, t("Record", S, N), O({})));
add("extends infer", t("Extends", {}, S, t("Infer", "U")));
add("extends infer in a tuple", t("Extends", {}, t("Tuple", [S, N]), t("Tuple", [t("Infer", "A"), t("Infer", "B")])));
add("extends rest infer in a tuple", t("Extends", {}, t("Tuple", [S, N, B]), t("Tuple", [S, t("Rest", t("Infer", "R"))])));
add("extends infer in an array", t("Extends", {}, t("Tuple", [S, N]), Arr(t("Infer", "E"))));
add("extends infer in a union", t("Extends", {}, U(S, N), t("Infer", "U")));
add("extends template literal", t("Extends", {}, t("TemplateLiteral", "a${'x'|'y'}"), S));
add("extends enum", t("Extends", {}, t("Enum", ["a", "b"]), S));
add("extends intersect", t("Extends", {}, t("Intersect", [O({ a: S }), O({ b: N })]), O({ a: S })));
add("extends to an intersect", t("Extends", {}, O({ a: S, b: N }), t("Intersect", [O({ a: S }), O({ b: N })])));
add("extends bigint", t("Extends", {}, t("BigInt"), t("BigInt")));
add("extends undefined void", t("Extends", {}, t("Undefined"), t("Void")));
add("extends symbol", t("Extends", {}, t("Symbol"), t("Symbol")));
add("extends null", t("Extends", {}, t("Null"), t("Null")));
add("extends boolean literal", t("Extends", {}, L(true), B));
add("extends number literal", t("Extends", {}, L(1), N));
add("extends unknown string", t("Extends", {}, t("Unknown"), S));
add("extends dependent", t("Extends", {}, t("Dependent", S, N, B), S));
add("extends a cyclic", t("Extends", {}, t("Cyclic", { A: O({ a: t("Ref", "A") }) }, "A"), O({})));
add("extends an unsafe", t("Extends", {}, t("Unsafe", { type: "string" }), t("Unknown")));
add("extends with inferred members in the context", t("Extends", { T: S }, S, S));
add("conditional true", t("Conditional", S, S, L(1), L(2)));
add("conditional false", t("Conditional", S, N, L(1), L(2)));
add("conditional with any is a union", t("Conditional", t("Any"), S, L(1), L(2)));
add("conditional with options", t("Conditional", S, S, L(1), L(2), { description: "d" }));
add("conditional with infer", t("Conditional", t("Tuple", [S, N]), t("Tuple", [t("Infer", "A"), t("Infer", "B")]), t("Ref", "B"), t("Never")));
add("conditional with a ref is deferred", t("Conditional", t("Ref", "T"), S, L(1), L(2)));
add("mapped over a union of literals", t("Mapped", t("Identifier", "K"), U(L("a"), L("b")), t("Ref", "K"), t("Ref", "K")));
add("mapped to a fixed property", t("Mapped", t("Identifier", "K"), U(L("a"), L("b")), t("Ref", "K"), S));
add("mapped over a number literal", t("Mapped", t("Identifier", "K"), L(1), t("Ref", "K"), N));
add("mapped over keyof", t("Mapped", t("Identifier", "K"), t("KeyOf", O({ a: S, b: N })), t("Ref", "K"), B));
add("mapped over a template literal", t("Mapped", t("Identifier", "K"), t("TemplateLiteral", "x${'a'|'b'}"), t("Ref", "K"), S));
add("mapped over an enum", t("Mapped", t("Identifier", "K"), t("Enum", ["a", "b"]), t("Ref", "K"), S));
add("mapped over a non-literal is empty", t("Mapped", t("Identifier", "K"), S, t("Ref", "K"), S));
add("mapped with optional", t("Mapped", t("Identifier", "K"), U(L("a")), t("Ref", "K"), opt(S)));
add("mapped with options", t("Mapped", t("Identifier", "K"), U(L("a")), t("Ref", "K"), S, { description: "d" }));
add("mapped over a ref is deferred", t("Mapped", t("Identifier", "K"), t("Ref", "X"), t("Ref", "K"), S));
add("interface with heritage", t("Interface", [O({ a: S })], { b: N }));
add("interface without heritage", t("Interface", [], { b: N, c: opt(S) }));
add("interface with options", t("Interface", [], { b: N }, { description: "d" }));
add("module of declarations", t("Module", { A: S, B: Arr(t("Ref", "A")) }));
add("module with a cyclic declaration", t("Module", { A: O({ next: opt(t("Ref", "A")) }), B: t("Ref", "A") }));
add("module with mutual recursion", t("Module", { A: O({ b: opt(t("Ref", "B")) }), B: O({ a: opt(t("Ref", "A")) }) }));
add("module with options", t("Module", { A: S }, { description: "d" }));
add("module of an empty set", t("Module", {}));
add("instantiate a ref from the context", t("Instantiate", { A: S }, t("Ref", "A")));
add("instantiate a missing ref", t("Instantiate", {}, t("Ref", "A")));
add("instantiate an object with refs", t("Instantiate", { A: S, B: N }, O({ a: t("Ref", "A"), b: opt(t("Ref", "B")) })));
add("instantiate a deferred pick", t("Instantiate", { A: P }, t("Pick", t("Ref", "A"), ["a"])));
add("instantiate a deferred partial", t("Instantiate", { A: P }, t("Partial", t("Ref", "A"))));
add("instantiate a deferred keyof", t("Instantiate", { A: P }, t("KeyOf", t("Ref", "A"))));
add("instantiate a deferred with", t("Instantiate", { A: S }, t("With", t("Ref", "A"), { description: "d" })));
add("instantiate a union of refs", t("Instantiate", { A: S, B: N }, U(t("Ref", "A"), t("Ref", "B"))));
add("instantiate a generic call", t("Instantiate", { Id: t("Generic", [t("Parameter", "T")], t("Ref", "T")) }, t("Call", t("Ref", "Id"), [S])));
add("instantiate a generic call with a constraint violation throws", t("Instantiate", { Id: t("Generic", [t("Parameter", "T", N)], t("Ref", "T")) }, t("Call", t("Ref", "Id"), [S])));
add("instantiate a distributive generic", t("Instantiate", { F: t("Generic", [t("Parameter", "T")], t("Conditional", t("Ref", "T"), S, L(1), L(2))) }, t("Call", t("Ref", "F"), [U(S, N)])));
add("instantiate a generic with a default", t("Instantiate", { D: t("Generic", [t("Parameter", "T", t("Unknown"), S)], Arr(t("Ref", "T"))) }, t("Call", t("Ref", "D"), [])));
add("instantiate a mapped generic", t("Instantiate", { M: t("Generic", [t("Parameter", "T")], t("Mapped", t("Identifier", "K"), t("KeyOf", t("Ref", "T")), t("Ref", "K"), S)) }, t("Call", t("Ref", "M"), [O({ a: N, b: B })])));
add("instantiate a recursive generic", t("Instantiate", { R: t("Generic", [t("Parameter", "T")], t("Ref", "T")), X: t("Call", t("Ref", "R"), [t("Call", t("Ref", "R"), [S])]) }, t("Ref", "X")));
add("call of a non generic ref", t("Call", t("Ref", "A"), [S]));
add("with of a non-ref", t("With", S, { description: "d" }));
// The tool parameters the conformance fixtures register in every SDK for the engine members (TestConformance_TypeEngineMembersAgreeAcrossSDKs): one property per member.
{
  const base = O({ id: S, name: opt(S), n: I, tags: Arr(S) });
  const same = O({ a: S });
  const key = t("Identifier", "K");
  add("conformance engine tool parameters", O({
    picked: t("Pick", base, ["id", "name"]),
    omitted: t("Omit", base, ["tags"]),
    partial: t("Partial", base),
    required: t("Required", O({ a: opt(S), b: opt(N) })),
    keys: t("KeyOf", base),
    indexed: t("Index", base, L("n")),
    readonlyObject: t("ReadonlyObject", base),
    readonlyType: t("ReadonlyType", Arr(S)),
    evaluated: t("Evaluate", t("Intersect", [same, O({ b: N })])),
    excluded: t("Exclude", U(S, N, B), S),
    extracted: t("Extract", U(S, N, B), U(S, N)),
    nonNullable: t("NonNullable", U(S, t("Null"))),
    uppercase: t("Uppercase", L("abc")),
    lowercase: t("Lowercase", L("ABC")),
    capitalize: t("Capitalize", L("abc")),
    uncapitalize: t("Uncapitalize", L("ABC")),
    parameters: t("Parameters", t("Function", [S, N], B)),
    constructorParameters: t("ConstructorParameters", t("Constructor", [S], O({}))),
    returnType: t("ReturnType", t("Function", [S], N)),
    instanceType: t("InstanceType", t("Constructor", [S], same)),
    fn: t("Function", [S], N),
    constructorType: t("Constructor", [S], same),
    tuple: t("Tuple", [S, N]),
    template: t("TemplateLiteral", "a${string}"),
    conditional: t("Conditional", S, S, L("yes"), L("no")),
    mapped: t("Mapped", key, U(L("a"), L("b")), t("Ref", "K"), S),
    cyclic: t("Cyclic", { A: O({ next: opt(t("Ref", "A")) }) }, "A"),
    module: t("Module", { A: S, B: Arr(t("Ref", "A")) }),
    interface: t("Interface", [same], { b: N }),
    dependent: t("Dependent", S, N, B),
    extends: t("Extends", {}, S, S),
    instantiate: t("Instantiate", { A: S }, t("Ref", "A")),
    call: t("Instantiate", { Id: t("Generic", [t("Parameter", "T")], t("Ref", "T")) }, t("Call", t("Ref", "Id"), [S])),
    script: t("Script", "{ a: string; b?: number[] }"),
    rest: t("Rest", t("Tuple", [S])),
    this: t("This"),
    infer: t("Infer", "U", S),
    identifier: key,
    generic: t("Generic", [t("Parameter", "T", S, S)], Arr(t("Ref", "T"))),
    recordKey: t("RecordKey", t("Record", S, N)),
    recordValue: t("RecordValue", t("Record", S, N)),
    recordPattern: t("String", { description: t("RecordPattern", t("Record", S, N)) }),
    enumKeyRecord: t("Record", t("Enum", ["x", "y"]), N),
  }, { additionalProperties: false }));
}
// Script: TypeScript type text.
const sc = (name, ...args) => add(`script ${name}`, t("Script", ...args));
sc("string", "string");
sc("number", "number");
sc("boolean", "boolean");
sc("null", "null");
sc("undefined", "undefined");
sc("any", "any");
sc("unknown", "unknown");
sc("never", "never");
sc("void", "void");
sc("symbol", "symbol");
sc("bigint", "bigint");
sc("integer", "integer");
sc("object", "object");
sc("string literal", "'a'");
sc("double quoted literal", '"a b"');
sc("number literal", "42");
sc("negative number literal", "-1.5");
sc("leading dot number literal", ".5");
sc("number literal with underscores", "1_000");
sc("boolean literal", "true");
sc("array", "string[]");
sc("array of arrays", "number[][]");
sc("tuple", "[string, number]");
sc("empty tuple", "[]");
sc("tuple with named elements", "[a: string, b?: number]");
sc("tuple with a rest element", "[string, ...number[]]");
sc("tuple with an optional element", "[string, number?]");
sc("tuple with a readonly element", "[readonly string]");
sc("union", "string | number");
sc("leading pipe union", "| string | number");
sc("intersection", "{ a: string } & { b: number }");
sc("union of intersections", "A | B & C");
sc("parenthesized", "(string | number)[]");
sc("object literal", "{ a: string; b?: number }");
sc("object with commas", "{ a: string, b: number }");
sc("object with newlines", "{\n a: string\n b: number\n}");
sc("object with readonly", "{ readonly a: string }");
sc("object with quoted keys", "{ 'a-b': string, \"c d\": number }");
sc("object with number keys", "{ 1: string, 0: number }");
sc("object with an index signature", "{ [key: string]: number }");
sc("object with a number index signature", "{ [key: number]: string; a: boolean }");
sc("empty object", "{}");
sc("nested object", "{ a: { b: { c: string } } }");
sc("keyof", "keyof { a: string; b: number }");
sc("keyof of a ref", "keyof T");
sc("indexed access", "{ a: string; b: number }['a']");
sc("indexed access by a union", "{ a: string; b: number }['a' | 'b']");
sc("array index access", "string[][number]");
sc("function", "(a: string, b?: number) => boolean");
sc("function with a rest parameter", "(...args: string[]) => void");
sc("constructor", "new (a: string) => { a: string }");
sc("conditional", "string extends string ? 1 : 2");
sc("conditional false branch", "string extends number ? 1 : 2");
sc("conditional with infer", "[1, 2] extends [infer A, ...infer B] ? A : never");
sc("conditional with a constrained infer", "[1, 2] extends [infer A extends number, infer B] ? B : never");
sc("conditional union distribution", "'a' | 'b' extends 'a' ? 1 : 2");
sc("mapped type", "{ [K in 'a' | 'b']: K }");
sc("mapped type with optional", "{ [K in 'a' | 'b']?: string }");
sc("mapped type with minus optional", "{ [K in keyof { a?: string }]-?: number }");
sc("mapped type with readonly", "{ readonly [K in 'a']: string }");
sc("mapped type with as", "{ [K in 'a' | 'b' as `x_${K}`]: K }");
sc("template literal", "`a${string}b`");
sc("template literal with a union", "`${'a' | 'b'}-${1 | 2}`");
sc("template literal without interpolation", "`abc`");
sc("Array reference", "Array<string>");
sc("Partial reference", "Partial<{ a: string; b: number }>");
sc("Required reference", "Required<{ a?: string }>");
sc("Pick reference", "Pick<{ a: string; b: number }, 'a'>");
sc("Omit reference", "Omit<{ a: string; b: number }, 'a'>");
sc("Record reference", "Record<string, number>");
sc("Record of a union reference", "Record<'a' | 'b', number>");
sc("Exclude reference", "Exclude<'a' | 'b', 'a'>");
sc("Extract reference", "Extract<'a' | 'b', 'a'>");
sc("NonNullable reference", "NonNullable<string | null>");
sc("Uppercase reference", "Uppercase<'a'>");
sc("Capitalize reference", "Capitalize<'ab'>");
sc("Parameters reference", "Parameters<(a: string) => void>");
sc("ReturnType reference", "ReturnType<() => string>");
sc("InstanceType reference", "InstanceType<new () => { a: string }>");
sc("ConstructorParameters reference", "ConstructorParameters<new (a: string) => void>");
sc("Readonly reference", "Readonly<{ a: string }>");
sc("Evaluate reference", "Evaluate<{ a: string } & { b: number }>");
sc("Index reference", "Index<{ a: string }, 'a'>");
sc("KeyOf reference", "KeyOf<{ a: string }>");
sc("readonly array", "readonly string[]");
sc("this", "this");
sc("generic type", "<T> = T[]");
sc("generic type with a constraint", "<T extends string> = T");
sc("generic type with a default", "<T = string> = T");
sc("generic type with a constraint and default", "<T extends string = 'a'> = T");
sc("type alias module", "type A = string; type B = A[]");
sc("exported module", "export type A = string\nexport type B = number");
sc("interface module", "interface A { a: string }\ninterface B extends A { b: number }");
sc("interface with an index signature", "interface A { [key: string]: number; a: number }");
sc("generic alias module", "type Id<T> = T\ntype X = Id<string>");
sc("generic interface module", "interface Box<T> { value: T }\ntype X = Box<string>");
sc("recursive module", "type A = { next: A | null }");
sc("mutually recursive module", "type A = { b: B }\ntype B = { a: A | null }");
sc("module with a generic call", "type Pair<A, B> = [A, B]\ntype X = Pair<string, number>");
sc("with clause", "string with { minLength: 1, description: 'd' }");
sc("with clause with values", "number with { minimum: 0, examples: [1, 2, 3], x: null, y: true, z: { w: 'q' } }");
sc("with clause on an object", "{ a: string } with { additionalProperties: false }");
sc("comments", "// a comment\n{ a: string /* inline */ }");
sc("trailing text is ignored", "string garbage");
sc("empty input is never", "");
sc("invalid input is never", "@@@");
sc("unterminated string is never", "'abc");
sc("unbalanced object is never", "{ a: string");
sc("an unresolved reference is a ref", "Foo");
sc("a keyword prefix", "stringy");
sc("with a context", { A: S, B: N }, "A | B");
sc("with a context and options", { A: S }, "A[]", { description: "d" });
sc("with options", "string", { description: "d" });
sc("with a context given as the first of two arguments", { A: S }, "A");
sc("with no arguments throws");
sc("with four arguments throws", {}, "string", {}, 1);
sc("a very large number literal is not finite", "1" + "0".repeat(400));
sc("whitespace and newlines", "\n\n  string  \n");
sc("a template literal in a record key", "Record<`a${'x' | 'y'}`, number>");
sc("a record with a template key", "{ [key: `a${string}`]: number }");
sc("deep nesting", "{ a: { b: [{ c: string[] }, number] } }[ 'a' ]");
sc("optional property in a Partial of an intersection", "Partial<{ a: string } & { b: number }>");
sc("union keyof", "keyof ({ a: string } | { a: number; b: string })");
sc("an indexed access on a tuple", "[string, number][1]");
sc("a conditional on a record", "{ a: string } extends Record<string, string> ? 1 : 2");
sc("a nested conditional", "string extends number ? 1 : string extends string ? 2 : 3");
sc("a mapped type over a keyof of a ref context", { O: O({ a: S, b: N }) }, "{ [K in keyof O]: O[K] }");
// A bigint in a schema: TypeBox's value holds it and JSON.stringify of the value throws "Do not know how to serialize a BigInt" (a result with "stringifyThrows" and no "json").
const big = (text) => ({ $: "bigint", value: text });
sc("a bigint literal", "1n");
sc("a negative bigint literal", "-12345678901234567890n");
sc("a bigint literal in an object", "{ a: 1n; b: string }");
sc("a union of bigint literals", "1n | 2n");
sc("a tuple of a bigint literal", "[1n, string]");
add("a pick of an object with a bigint literal", t("Pick", t("Script", "{ a: 1n; b: string }"), ["b"]));
add("a pick that keeps a bigint literal", t("Pick", t("Script", "{ a: 1n; b: string }"), ["a"]));
add("a keyof of an object with a bigint literal", t("KeyOf", t("Script", "{ a: 1n; b: string }")));
add("literal of a bigint", t("Literal", big("7")));
add("literal of a large bigint", t("Literal", big("123456789012345678901234567890")));
add("literal of a bigint with options", t("Literal", big("7"), { description: "d" }));
add("union of bigint literals", t("Union", [t("Literal", big("1")), t("Literal", big("2"))]));
add("object with a bigint literal property", t("Object", { a: t("Literal", big("1")), b: S }));
add("IsLiteral of a bigint literal", t("IsLiteral", t("Literal", big("1"))));
add("IsKind Literal of a bigint literal", t("IsKind", t("Literal", big("1")), "Literal"));
add("an optional bigint literal", t("Optional", t("Literal", big("1"))));

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
    if (node.$ === "bigint") return BigInt(node.value);
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
// The wire form of a value (internal/typebox/engine/wire.go): JSON, with an envelope for undefined, bigint, functions and the hidden members of an object.
export const toWire = (v) => {
  const env = (kind, rest = "") => `{"\\u0000":"${kind}"${rest}}`;
  if (v === undefined || typeof v === "function" || typeof v === "symbol") return env("u");
  if (typeof v === "bigint") return env("n", `,"v":"${v}"`);
  if (v === null || typeof v !== "object") return JSON.stringify(v) ?? "null";
  if (Array.isArray(v)) return `[${v.map(toWire).join(",")}]`;
  const enumerable = Object.keys(v);
  const hidden = Object.getOwnPropertyNames(v).filter((k) => !enumerable.includes(k));
  const body = (keys) => `{${keys.map((k) => `${JSON.stringify(k)}:${toWire(v[k])}`).join(",")}}`;
  if (hidden.length === 0 && !enumerable.some((k) => k.startsWith("\u0000"))) return body(enumerable);
  return env("o", `${hidden.length ? `,"h":${body(hidden)}` : ""},"e":${body(enumerable)}`);
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
  const result = {};
  try {
    result.json = JSON.stringify(value);
  } catch (error) {
    result.stringifyThrows = String(error.message); // a bigint member: the value is fine, writing it is not
  }
  result.hidden = hidden;
  result.wire = toWire(value);
  if (typeof node.$call === "string") result.wireArgs = node.args.map((x) => toWire(resolve(Type, x)));
  return result;
};
