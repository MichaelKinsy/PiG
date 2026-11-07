// SPDX-License-Identifier: MIT
// A fixture extension for bench/bolt-aot/equivalence_pkgs.py: it finds its data and its skills beside its own modules, every way the Piglet's rewrite covers,
// and reads them when its tool runs. On a compiled host without the rewrite those reads find nothing under /$bunfs; with it they find the installed files.
import { readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import legacy from "./legacy.cjs";

// The part of the extension API this fixture uses; like the other bench fixtures it imports nothing from Pi.
interface Api {
  registerTool(tool: unknown): void;
  registerCommand(name: string, command: unknown): void;
}

const here = dirname(fileURLToPath(import.meta.url));
const requireHere = createRequire(import.meta.url);

export default function (pi: Api) {
  pi.registerTool({
    name: "locating_read",
    label: "Locating read",
    description: "Read the data files that sit beside this extension. Used by the rewrite equivalence check only.",
    parameters: { type: "object", properties: { name: { type: "string" } }, additionalProperties: false },
    async execute(_id: string, params: { name?: string }) {
      const name = params.name ?? "note.txt";
      const answer = {
        here,
        viaPath: readFileSync(join(here, "data", name), "utf8"),
        viaUrl: readFileSync(new URL(`./data/${name}`, import.meta.url), "utf8"),
        viaDirname: readFileSync(join(import.meta.dirname, "data", name), "utf8"),
        skills: readdirSync(join(here, "skills")).sort(),
        skillA: readFileSync(join(here, "skills", "a.md"), "utf8"),
        legacy: legacy.read(),
        legacyFile: legacy.file(),
        resolved: basename(fileURLToPath(import.meta.resolve("./data/note.txt"))),
        required: basename(requireHere.resolve("./legacy.cjs")),
        mutated: process.env.LOCATING_MUTATE === "1",
      };
      return { content: [{ type: "text", text: JSON.stringify(answer) }], details: { name } };
    },
  });
  pi.registerCommand("locating", { description: "Locating fixture command", handler: async () => {} });
}
