// JSON Schema validation (drafts 2020-12 and 7) without eval: compile() builds a schema into plain closures once.
// The builders return plain JSON Schema objects typed with the values they accept.
export { arr, bigint, bool, bytes, int, lit, map, nul, nullable, num, Optional, obj, oneOf, opt, recursive, str, tuple, union, unknown, unsafe, } from "./builders.js";
export { deepEqual as equal } from "./checks.js";
export { CoercingValidator, compile, Validator } from "./validator.js";
//# sourceMappingURL=index.js.map