// parity-patch-file: registers `patch_file`, an edit-style extension tool that wraps its whole read-modify-write in withFileMutationQueue, as docs/extensions.md asks
// ("File-mutating tools should wrap the complete read-modify-write operation with withFileMutationQueue()"). A pause between the read and the write keeps the file
// stale for any mutation that is not serialized with it.
import { withFileMutationQueue } from "@earendil-works/pi-coding-agent";
import { readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

export default function (pi) {
  pi.registerTool({
    name: "patch_file",
    label: "patch_file",
    description: "Append one line to a file under the file mutation queue.",
    parameters: {
      type: "object",
      properties: {
        path: { type: "string", description: "File to append to." },
        line: { type: "string", description: "Line to append." },
      },
      required: ["path", "line"],
    },
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const file = resolve(ctx?.cwd ?? process.cwd(), params.path);
      await withFileMutationQueue(file, async () => {
        const text = await readFile(file, "utf8");
        await new Promise((done) => setTimeout(done, 200));
        await writeFile(file, `${text}${params.line}\n`);
      });
      return { content: [{ type: "text", text: `patch-file: appended ${params.line}` }] };
    },
  });
}
