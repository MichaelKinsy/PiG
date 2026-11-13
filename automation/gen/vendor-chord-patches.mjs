// PiG's patches over a vendored chord tree, shared by vendor-chord-plugins-node.mjs and vendor-chord-surfaces-client.mjs: an
// approved change to one of Pi's own files that Pi has not made (a `carried` commit is Pi's; a patch is PiG's).
//
// A patch is a file of automation/gen/chord-patches named `<record>-<name>.patch` (D636-apply-acquired-pick.patch): a plain
// unified diff against Pi's file as the manifest's commit and carried commits make it, with Pi's paths
// (`packages/chord/src/...`). Its first line names its record again (`D636: ...`): a divergence of docs/parity/DIVERGENCES.md
// or an additive feature of docs/additive-features.md that says SCRUTINIZED:approved. A patch without such a record is
// refused, so nothing rides in unrecorded.
//
// The patches are applied after the copy, in the order of their names, to the files a tree carries. A hunk applies only where
// its line numbers say, and only when every context and removed line is the file's (no offset, no fuzz); otherwise the
// vendoring fails, so a re-vendor at another Pi commit keeps a patch or drops it knowingly. The manifest lists the patches
// applied to its tree as `patches` (the name and the sha256 of the patch file; an empty list when there is none), and its file
// hashes are of the patched files. `make vendor-chord-carried-check` regenerates with them.
//
//   node vendor-chord-patches.mjs <patch dir> <tree root> <record file>...
// applies the patches of a directory to the files under a root (the patches' paths joined to it) and prints what it
// applied: the form the tests run (chord/nextnode/surfaces/client_patches_test.go).
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
/** Where the patches live. */
export const PATCH_DIR = join(here, "chord-patches");
/** The records that approve them. */
export const RECORDS = [join(here, "../../docs/parity/DIVERGENCES.md"), join(here, "../../docs/additive-features.md")];

/** The body of record `id` in `text` (from its `## <id> ...` heading to the next heading of its level), or undefined. */
function recordBody(text, id) {
	const heading = new RegExp(`^(#{2,3}) ${id}\\b.*$`, "m").exec(text);
	if (heading === null) return undefined;
	const rest = text.slice(heading.index + heading[0].length);
	const next = rest.search(new RegExp(`^#{1,${heading[1].length}} `, "m"));
	return next < 0 ? rest : rest.slice(0, next);
}

/** Throws unless one of `records` (files) has a record `id` that says SCRUTINIZED:approved. */
function requireApproved(id, name, records) {
	for (const file of records) {
		if (!existsSync(file)) continue;
		if (recordBody(readFileSync(file, "utf8"), id)?.includes("SCRUTINIZED:approved")) return;
	}
	throw new Error(`${name}: its record ${id} is not SCRUTINIZED:approved in docs/parity/DIVERGENCES.md or docs/additive-features.md`);
}

/** A patch file parsed: its record, and for each of Pi's paths its hunks (the old and the new lines of each, from `start`). */
function parsePatch(name, text) {
	const id = /^([A-Z]+[0-9]+)-[a-z0-9-]+\.patch$/.exec(name)?.[1];
	if (id === undefined) throw new Error(`${name}: a patch is named <record>-<name>.patch, as D636-apply-acquired-pick.patch`);
	const lines = text.split("\n");
	if (!lines[0].startsWith(`${id}: `)) throw new Error(`${name}: its first line names its record: "${id}: ..."`);
	const files = [];
	let file;
	let hunk;
	for (let at = 1; at < lines.length; at++) {
		const line = lines[at];
		if (line.startsWith("--- ")) {
			const from = /^--- a\/(\S+)$/.exec(line)?.[1];
			const to = /^\+\+\+ b\/(\S+)$/.exec(lines[++at] ?? "")?.[1];
			if (from === undefined || from !== to) throw new Error(`${name}:${at}: a file's header is "--- a/<path>" then "+++ b/<the same path>"`);
			file = { path: from, hunks: [] };
			files.push(file);
			hunk = undefined;
		} else if (line.startsWith("@@ ")) {
			const counts = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
			if (counts === null || file === undefined) throw new Error(`${name}:${at + 1}: not the header of a file's hunk`);
			hunk = { start: Number(counts[1]), oldCount: Number(counts[2] ?? 1), newCount: Number(counts[4] ?? 1), old: [], new: [] };
			file.hunks.push(hunk);
		} else if (hunk !== undefined && /^[ +-]/.test(line)) {
			if (!line.startsWith("+")) hunk.old.push(line.slice(1));
			if (!line.startsWith("-")) hunk.new.push(line.slice(1));
		} else if (hunk !== undefined && !(line === "" && at === lines.length - 1)) {
			throw new Error(`${name}:${at + 1}: not a line of a hunk`);
		}
		// Before the first file: the patch's own text (its record line, a description).
	}
	if (files.length === 0 || files.some((each) => each.hunks.length === 0)) throw new Error(`${name}: a patch has files, and each file hunks`);
	for (const each of files) {
		for (const part of each.hunks) {
			if (part.old.length !== part.oldCount || part.new.length !== part.newCount) {
				throw new Error(`${name}: the hunk of ${each.path} at line ${part.start} has not the lines its header counts`);
			}
		}
	}
	return { id, files };
}

/**
 * Applies the patches of `dir`, in the order of their names, to the files `locate` finds (`locate(path)` is the copy of Pi's
 * `path` in the tree, or undefined when the tree does not carry it); returns the manifest's `patches`: the patches that
 * changed a file of the tree, each with its name and the sha256 of its file.
 */
export function applyPatches(locate, dir = PATCH_DIR, records = RECORDS) {
	const applied = [];
	if (!existsSync(dir)) return applied;
	for (const name of readdirSync(dir).filter((each) => each.endsWith(".patch")).sort()) {
		const bytes = readFileSync(join(dir, name));
		const patch = parsePatch(name, bytes.toString("utf8"));
		requireApproved(patch.id, name, records);
		let changed = false;
		for (const file of patch.files) {
			const target = locate(file.path);
			if (target === undefined) continue;
			const lines = readFileSync(target, "utf8").split("\n");
			// From the last hunk to the first: a hunk's line numbers are those of the file before the patch.
			for (const part of [...file.hunks].sort((a, b) => b.start - a.start)) {
				const found = lines.slice(part.start - 1, part.start - 1 + part.old.length);
				if (found.length !== part.old.length || found.some((line, at) => line !== part.old[at])) {
					throw new Error(`${name}: does not apply to ${file.path} at line ${part.start}: the file is not the one the patch was made against (no offset, no fuzz)`);
				}
				lines.splice(part.start - 1, part.old.length, ...part.new);
			}
			writeFileSync(target, lines.join("\n"));
			changed = true;
		}
		if (changed) applied.push({ name, sha256: createHash("sha256").update(bytes).digest("hex") });
	}
	return applied;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const [dir, root, ...records] = process.argv.slice(2);
	if (root === undefined) throw new Error("usage: vendor-chord-patches.mjs <patch dir> <tree root> <record file>...");
	console.log(JSON.stringify(applyPatches((path) => (existsSync(join(root, path)) ? join(root, path) : undefined), dir, records)));
}
