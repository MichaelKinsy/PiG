// What `typedChange` (the vendored terminal-input.ts, Node strips its types) makes of each edit of the terminal's textarea:
// prints [before, after, change] triples as JSON for e2e/terminal_web_test.go.
import { typedChange } from "../../web/upstream/plugins/terminals/terminal-input.ts";

const cases = JSON.parse(process.argv[2]);
console.log(JSON.stringify(cases.map(([before, after]) => typedChange(before, after))));
