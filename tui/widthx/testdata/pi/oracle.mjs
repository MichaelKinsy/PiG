// Reads a JSON corpus from argv[2] and writes upstream results to stdout.
import fs from "node:fs";
const u = await import(process.argv[3]);
const corpus = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const seg = new Intl.Segmenter(undefined, { granularity: "grapheme" });
const out = { unicode: process.versions.unicode, node: process.version, width: [], segs: [], trunc: [], wrap: [], slice: [], extract: [] };
for (const s of corpus.width) out.width.push(u.visibleWidth(s));
for (const s of corpus.segs) out.segs.push([...seg.segment(s)].map((x) => x.segment));
for (const [s, w, e, p] of corpus.trunc) out.trunc.push(u.truncateToWidth(s, w, e, p));
for (const [s, w] of corpus.wrap) out.wrap.push(u.wrapTextWithAnsi(s, w));
for (const [s, a, n, strict] of corpus.slice || []) out.slice.push(u.sliceWithWidth(s, a, n, strict));
for (const [s, be, as, al, strict] of corpus.extract || []) out.extract.push(u.extractSegments(s, be, as, al, strict));
process.stdout.write(JSON.stringify(out));
