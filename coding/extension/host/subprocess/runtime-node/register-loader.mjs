import { register } from "node:module";
import { pathToFileURL } from "node:url";

register(new URL("./loader.mjs", import.meta.url), pathToFileURL(process.cwd() + "/"));
