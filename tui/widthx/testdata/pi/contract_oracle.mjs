// Reads a JSON corpus from argv[2] and writes the upstream results of the wrapping and truncation functions of
// utils.ts to stdout, including the module-private ones, which the Go test exports by editing the source text.
import fs from "node:fs";
const u = await import(process.argv[3]);
const corpus = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const out = { tokens: [], wrapLine: [], breakWord: [], wrap: [], trunc: [] };
for (const s of corpus.tokens ?? []) out.tokens.push(u.splitIntoTokensWithAnsi(s));
for (const [s, w] of corpus.wrapLine ?? []) out.wrapLine.push(u.wrapSingleLine(s, w));
for (const [word, w, before] of corpus.breakWord ?? []) {
  const tracker = new u.AnsiCodeTracker();
  u.updateTrackerFromText(before, tracker);
  const lines = u.breakLongWord(word, w, tracker);
  out.breakWord.push({ lines, active: tracker.getActiveCodes(), reset: tracker.getLineEndReset() });
}
for (const [s, w] of corpus.wrap ?? []) out.wrap.push(u.wrapTextWithAnsi(s, w));
for (const [s, w, e, p] of corpus.trunc ?? []) out.trunc.push(u.truncateToWidth(s, w, e, p));
process.stdout.write(JSON.stringify(out));
