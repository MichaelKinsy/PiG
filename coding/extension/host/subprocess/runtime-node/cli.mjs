import { loadExtension } from "./runtime.mjs";

const entry = process.argv[2];
if (!entry) {
  console.error("usage: node cli.mjs <extension-entry>");
  process.exit(1);
}

try {
  await loadExtension(entry);
} catch (err) {
  console.error(err?.stack || String(err));
  process.exit(1);
}
