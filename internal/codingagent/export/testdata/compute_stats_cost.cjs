// Runs computeStats from the exported template, as packages/coding-agent/test/export-html-cost.test.ts does (pi-mono commit 83ca8020).
const fs = require("node:fs");
const vm = require("node:vm");

const templateJs = fs.readFileSync(process.argv[2], "utf-8");
const start = templateJs.indexOf("function computeStats(entryList)");
const end = templateJs.indexOf("const globalStats = computeStats(entries);");
if (start < 0 || end <= start) throw new Error("computeStats not found");
const entries = JSON.parse(process.argv[3]);
const context = {};
vm.runInNewContext(`${templateJs.slice(start, end)}\nresult = computeStats(${JSON.stringify(entries)});`, context);
process.stdout.write(JSON.stringify(context.result.cost));
