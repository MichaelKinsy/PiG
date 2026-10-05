/**
 * Output files: files pi writes so the model can use output it was not shown in full, such as the
 * full text of truncated tool output, binary MCP resources, and images shown by codemode scripts.
 * Every output file is created here, so where they are stored can change in one place. Today they
 * go to the OS temp directory.
 */
import { randomBytes } from "node:crypto";
import { createWriteStream } from "node:fs";
import { writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
/** Output can carry private data, so only the user may read the files. */
const OUTPUT_FILE_MODE = 0o600;
/** A new, unused path: `<dir>/<prefix>-<random hex><extension>`. `extension` includes the dot. */
function createOutputFilePath(prefix, extension) {
    return join(tmpdir(), `${prefix}-${randomBytes(8).toString("hex")}${extension}`);
}
/** Write `data` to a new output file and return its path. */
export async function writeOutputFile(prefix, extension, data) {
    const path = createOutputFilePath(prefix, extension);
    // `wx` never follows a link someone else placed at the path.
    await writeFile(path, data, { mode: OUTPUT_FILE_MODE, flag: "wx" });
    return path;
}
/** Open a new output file for streamed output. */
export function createOutputFileStream(prefix, extension) {
    const path = createOutputFilePath(prefix, extension);
    return { path, stream: createWriteStream(path, { mode: OUTPUT_FILE_MODE, flags: "wx" }) };
}
//# sourceMappingURL=output-files.js.map