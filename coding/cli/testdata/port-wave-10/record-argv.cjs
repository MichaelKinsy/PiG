// upstream: packages/coding-agent/test/package-command-paths.test.ts:378-380
const fs = require("node:fs");
fs.writeFileSync(process.env.WAVE10_NPM_RECORD, JSON.stringify(process.argv.slice(2)));
