// Import this first in an oracle script that must see the carried Pi fixes (D217); see README.md.
import { register } from "node:module";

register("./hook.mjs", import.meta.url);
