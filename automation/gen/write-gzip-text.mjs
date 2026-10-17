// Write generated text as a gzip file without spurious drift.
//
// zlib builds compress the same text to different bytes, and macOS's zlib writes a different OS code in the header, so a
// regenerated file would differ from the committed one on another platform although its content is the same. A file
// whose content is already the text is left alone; a new or changed file is written with the Unix OS code that Linux
// writes.
import fs from "node:fs";
import zlib from "node:zlib";

export function writeGzipText(file, text) {
	const data = Buffer.from(text);
	let existing;
	try {
		existing = fs.readFileSync(file);
	} catch (error) {
		if (error.code !== "ENOENT") throw error;
	}
	if (existing !== undefined) {
		let content;
		try {
			content = zlib.gunzipSync(existing);
		} catch {
			// Not valid gzip: replace it.
		}
		if (content !== undefined && content.equals(data)) return;
	}
	const gz = zlib.gzipSync(data, { level: 9 });
	gz[9] = 3;
	fs.writeFileSync(file, gz);
}
