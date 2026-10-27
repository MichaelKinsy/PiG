// pi-ai's validateToolArguments for tools whose parameters carry TypeBox refinements (Type.Refine). Pi calls each refinement while TypeBox validates: Check (Compile's generated code)
// stops at the first failing keyword or refinement, anyOf tries its branches in order and takes the first that passes, and when Check fails, Errors runs again, every check of a node
// whose keywords reported no error, with error(value) for each failure. A predicate that throws ends validateToolArguments with its message.
//
// An argument is [tool, toolCall] with tool.parameters in the DSL of Type.mjs. The answer is {"error": message} or {"args": validated arguments}, "calls" is the order the predicates and
// message functions were called in (each function's tag labels its call: "check:len", "error:lenmsg"), and "schema" and "sites" are the schema's JSON and the nodes that carry refinements
// ({path, refinements: [{check: spec, error: spec}]}), and "validation" is the form the schema is registered for validation in (see validationParameters), which the host's own traversal is
// tested with.
import { calls, predicateFor, resolve, specOf } from "./Type.mjs";

const t = (name, ...args) => ({ $call: name, args });
const fn = (name, spec = {}) => ({ $fn: name, ...spec });
const S = t("String"), N = t("Number"), I = t("Integer"), B = t("Boolean");
const opt = (x) => t("Optional", x);
const refine = (type, check, error) => (error ? t("Refine", type, check, error) : t("Refine", type, check));

// The tool the conformance fixtures register in every SDK (TestConformance_RefinedToolArgumentsAgreeAcrossSDKs): a refined string, an optional refined integer, a union whose
// branches are refined strings, an optional string that throws, and a refined root.
const probe = t(
  "Refine",
  t("Object", {
    name: refine(S, fn("lengthAtLeast", { n: 3, tag: "len" }), fn("text", { text: "name is too short", tag: "lenmsg" })),
    qty: opt(refine(I, fn("even", { tag: "even" }), fn("textWithValue", { prefix: "qty must be even, got ", tag: "evenmsg" }))),
    pick: t("Union", [
      refine(S, fn("equals", { value: "a", tag: "u1" }), fn("text", { text: "not a", tag: "u1m" })),
      refine(S, fn("equals", { value: "b", tag: "u2" }), fn("text", { text: "not b", tag: "u2m" })),
    ]),
    boom: opt(refine(S, fn("throws", { message: "kaboom", tag: "boom" }))),
  }),
  fn("always", { tag: "root" }),
  fn("text", { text: "root refused", tag: "rootmsg" }),
);

const cases = [];
const add = (name, parameters, args, toolName = "probe") => cases.push({ name, args: [{ name: toolName, parameters }, { type: "toolCall", id: "c1", name: toolName, arguments: args }] });

// the conformance fixture's tool
add("probe: everything passes", probe, { name: "abcd", qty: 4, pick: "a" });
add("probe: no optional members", probe, { name: "abc", pick: "b" });
add("probe: first union branch fails, the second rescues", probe, { name: "abc", pick: "b", qty: 2 });
add("probe: a refinement fails", probe, { name: "ab", pick: "a" });
add("probe: an optional refinement fails with the value in its message", probe, { name: "abc", qty: 3, pick: "a" });
add("probe: every union branch fails", probe, { name: "abc", pick: "c" });
add("probe: two refinements fail", probe, { name: "ab", qty: 5, pick: "a" });
add("probe: all three fail", probe, { name: "x", qty: 1, pick: "z" });
add("probe: a structural error beside a refined member", probe, { name: "abc", pick: 5 });
add("probe: a missing required member is not refined", probe, { pick: "a" });
add("probe: an optional null is dropped before it is refined", probe, { name: "abc", qty: null, pick: "a" });
add("probe: a string coerced to an integer is refined as the integer", probe, { name: "abc", qty: "4", pick: "a" });
add("probe: a predicate that throws", probe, { name: "abc", pick: "a", boom: "x" });
add("probe: a predicate that would throw is not reached after a failure above it", probe, { name: "ab", pick: "a", boom: "x" });

// the tool the packed conformance cells register (TestConformance_PackedRefinedToolArgumentsAcrossSDKs): one refined property
const packedParameters = t("Object", { a: refine(S, fn("lengthAtLeast", { n: 3, tag: "p" }), fn("text", { text: "a is too short", tag: "pm" })) });
add("packed: a refined property passes", packedParameters, { a: "abc" }, "t");
add("packed: a refined property fails", packedParameters, { a: "x" }, "t");

// traversal
add("two refinements on one node: the first fails, the second is not called by Check", refine(refine(S, fn("never", { tag: "r1" }), fn("text", { text: "one", tag: "m1" })), fn("never", { tag: "r2" }), fn("text", { text: "two", tag: "m2" })), "x", "t");
add("two refinements on one node: both pass", refine(refine(S, fn("always", { tag: "r1" })), fn("always", { tag: "r2" })), "x", "t");
add("a refinement whose node has a structural error is not called", refine(S, fn("never", { tag: "r" })), 5, "t");
add("a root refinement on an object", refine(t("Object", { a: S }), fn("never", { tag: "root" }), fn("text", { text: "no object", tag: "m" })), { a: "x" }, "t");
add("array items: the first failing item stops Check, Errors visits every item", t("Object", { xs: t("Array", refine(I, fn("even", { tag: "e" }), fn("textWithValue", { prefix: "odd ", tag: "m" }))) }), { xs: [2, 3, 5, 4] }, "t");
add("array items all pass", t("Object", { xs: t("Array", refine(I, fn("even", { tag: "e" }))) }), { xs: [2, 4] }, "t");
add("a refinement on the array itself", t("Object", { xs: refine(t("Array", I), fn("never", { tag: "arr" }), fn("text", { text: "bad list", tag: "m" })) }), { xs: [1] }, "t");
add("nested objects", t("Object", { a: t("Object", { b: refine(S, fn("never", { tag: "b" }), fn("text", { text: "deep", tag: "m" })) }) }), { a: { b: "x" } }, "t");
add("a parent refinement runs only when the children report no error", refine(t("Object", { a: refine(S, fn("never", { tag: "child" }), fn("text", { text: "child", tag: "cm" })) }), fn("never", { tag: "parent" }), fn("text", { text: "parent", tag: "pm" })), { a: "x" }, "t");
add("sibling properties: the first failing property stops Check", t("Object", { a: refine(S, fn("never", { tag: "a" }), fn("text", { text: "a bad", tag: "am" })), b: refine(S, fn("never", { tag: "b" }), fn("text", { text: "b bad", tag: "bm" })) }), { a: "x", b: "y" }, "t");
add("sibling properties: Errors reports both", t("Object", { a: refine(S, fn("always", { tag: "a" })), b: refine(S, fn("never", { tag: "b" }), fn("text", { text: "b bad", tag: "bm" })) }), { a: "x", b: "y" }, "t");
add("anyOf: the first branch passes, the others are not called", t("Object", { v: t("Union", [refine(S, fn("always", { tag: "u1" })), refine(S, fn("never", { tag: "u2" }))]) }), { v: "x" }, "t");
add("anyOf: all branches fail, Errors words every branch", t("Object", { v: t("Union", [refine(S, fn("never", { tag: "u1" }), fn("text", { text: "first", tag: "m1" })), refine(S, fn("never", { tag: "u2" }), fn("text", { text: "second", tag: "m2" }))]) }), { v: "x" }, "t");
add("anyOf: a branch of another type is not refined", t("Object", { v: t("Union", [refine(I, fn("never", { tag: "u1" })), refine(S, fn("always", { tag: "u2" }))]) }), { v: "x" }, "t");
add("anyOf inside anyOf", t("Object", { v: t("Union", [t("Union", [refine(S, fn("never", { tag: "i1" }), fn("text", { text: "i1", tag: "m1" })), refine(S, fn("never", { tag: "i2" }), fn("text", { text: "i2", tag: "m2" }))]), refine(S, fn("always", { tag: "o2" }))]) }), { v: "x" }, "t");
add("anyOf inside anyOf: the inner rescues", t("Object", { v: t("Union", [t("Union", [refine(S, fn("never", { tag: "i1" })), refine(S, fn("always", { tag: "i2" }))]), refine(S, fn("never", { tag: "o2" }))]) }), { v: "x" }, "t");
add("anyOf of objects with refined members", t("Object", { v: t("Union", [t("Object", { k: t("Literal", "a"), n: refine(I, fn("even", { tag: "n1" })) }), t("Object", { k: t("Literal", "b"), n: refine(I, fn("greaterThan", { n: 10, tag: "n2" }), fn("text", { text: "n too small", tag: "m2" })) })]) }), { v: { k: "b", n: 3 } }, "t");
add("allOf: every part is checked, in order", t("Intersect", [t("Object", { a: refine(S, fn("always", { tag: "a" })) }), t("Object", { b: refine(S, fn("never", { tag: "b" }), fn("text", { text: "b bad", tag: "bm" })) })]), { a: "x", b: "y" }, "t");
add("allOf: the first part fails, the second is not called by Check", t("Intersect", [t("Object", { a: refine(S, fn("never", { tag: "a" }), fn("text", { text: "a bad", tag: "am" })) }), t("Object", { b: refine(S, fn("always", { tag: "b" })) })]), { a: "x", b: "y" }, "t");
add("record values", t("Object", { m: t("Record", S, refine(I, fn("even", { tag: "v" }), fn("textWithValue", { prefix: "odd ", tag: "m" }))) }), { m: { x: 2, y: 3, z: 4 } }, "t");
add("record values all pass", t("Object", { m: t("Record", S, refine(I, fn("even", { tag: "v" }))) }), { m: { x: 2, y: 4 } }, "t");
add("a refined optional member that is absent is not called", t("Object", { a: opt(refine(S, fn("never", { tag: "a" }))) }), {}, "t");
add("a refined Unknown receives null where the property is optional", t("Object", { a: opt(refine(t("Unknown"), fn("never", { tag: "a" }), fn("text", { text: "unknown bad", tag: "m" }))) }), { a: null }, "t");
add("a throw: the check throws an Error", t("Object", { a: refine(S, fn("throws", { message: "boom", tag: "a" })) }), { a: "x" }, "t");
add("a throw: the check throws a string", t("Object", { a: refine(S, fn("throwsText", { message: "thrown text", tag: "a" })) }), { a: "x" }, "t");
add("a throw: the error function throws", t("Object", { a: refine(S, fn("never", { tag: "a" }), fn("errorThrows", { message: "message boom", tag: "m" })) }), { a: "x" }, "t");
add("a throw in a union branch that Check does not reach", t("Object", { v: t("Union", [refine(S, fn("always", { tag: "u1" })), refine(S, fn("throws", { message: "never reached", tag: "u2" }))]) }), { v: "x" }, "t");
add("a throw in the second union branch after the first fails", t("Object", { v: t("Union", [refine(S, fn("never", { tag: "u1" })), refine(S, fn("throws", { message: "second branch", tag: "u2" }))]) }), { v: "x" }, "t");
add("more than eight errors: Errors stops at capacity", t("Object", Object.fromEntries(["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"].map((k) => [k, refine(S, fn("never", { tag: k }), fn("text", { text: `bad ${k}`, tag: `m${k}` }))]))), Object.fromEntries(["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"].map((k) => [k, "x"])), "t");
add("a refinement under an if/then/else", t("Object", { a: S }, { if: { properties: { a: { const: "x" } } }, then: refine(t("Object", {}), fn("never", { tag: "then" }), fn("text", { text: "then bad", tag: "m" })), else: t("Object", {}) }), { a: "x" }, "t");
export { cases };

// refinement sites of a resolved schema, by the host's paths ("/properties/a", "/anyOf/1", "/items", ...)
const WITH_MEMBERS = ["properties", "patternProperties", "dependentSchemas", "dependencies", "$defs", "definitions"];
const WITH_LIST = ["allOf", "anyOf", "oneOf", "prefixItems", "items"];
const WITH_CHILD = ["additionalProperties", "additionalItems", "contains", "propertyNames", "not", "if", "then", "else", "items", "unevaluatedItems", "unevaluatedProperties"];
const pointer = (key) => key.replaceAll("~", "~0").replaceAll("/", "~1");
function sitesOf(schema) {
  const sites = [];
  const visit = (node, path) => {
    if (!node || typeof node !== "object" || Array.isArray(node)) return;
    const list = node["~refine"];
    if (Array.isArray(list) && list.length > 0) sites.push({ path, refinements: list.map((r) => ({ check: specOf.get(r.check), error: r.error && specOf.has(r.error) ? specOf.get(r.error) : null })) });
    for (const key of Object.keys(node)) {
      const value = node[key];
      if (WITH_MEMBERS.includes(key) && value && typeof value === "object") for (const name of Object.keys(value)) visit(value[name], `${path}/${key}/${pointer(name)}`);
      else if (WITH_LIST.includes(key) && Array.isArray(value)) value.forEach((member, index) => visit(member, `${path}/${key}/${index}`));
      else if (WITH_CHILD.includes(key)) visit(value, `${path}/${key}`);
    }
  };
  visit(schema, "");
  return sites;
}

// The parameters as the Node runtime sends them for validation (runtime.mjs toolDeclaration validation_parameters): the JSON with TypeBox's ~kind on every node, and for an Intersect,
// Enum or TemplateLiteral node the evaluated schema as ~convert, which the host's port of Value.Convert reads. An SDK sends the same form for a schema built with Type.
function validationParameters(Type, parameters) {
  return JSON.parse(JSON.stringify(parameters, (_key, value) => {
    if (value && typeof value === "object" && !Array.isArray(value) && value["~kind"]) {
      const copy = { ...value, "~kind": value["~kind"] };
      if (["Intersect", "Enum", "TemplateLiteral"].includes(value["~kind"])) copy["~convert"] = Type.Evaluate(value["~kind"] === "Intersect" ? Type.Instantiate({}, value) : value);
      return copy;
    }
    return value;
  }));
}

export const call = async function (validateToolArguments, tool, toolCall) {
  const { Type } = await this.load("pi-ai");
  const parameters = resolve(Type, tool.parameters);
  calls.length = 0;
  const out = {};
  try {
    out.args = JSON.parse(JSON.stringify(validateToolArguments({ name: tool.name, description: "d", parameters }, toolCall)));
  } catch (error) {
    out.error = error instanceof Error ? error.message : String(error);
  }
  out.calls = [...calls];
  out.schema = JSON.stringify(parameters);
  out.validation = JSON.stringify(validationParameters(Type, parameters));
  out.sites = sitesOf(parameters);
  return out;
};
