// Registers the loader hook below: Node refuses to strip the types of a file under node_modules, and the vendored pi-schema
// (Pi's package, TypeScript sources) lives there (client/pi/node_modules).
import { register } from "node:module";

register("./strip-hooks.mjs", import.meta.url);
