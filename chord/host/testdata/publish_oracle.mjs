// Run Pi's publication queue (packages/chord/src/publish.ts, imported as-is with types stripped) over programs from stdin.
// A program is {tasks: [actions], top: actions}; an action is {log: label} | {enqueue: task index} | {hold: {host, actions}}.
// enqueue(() => run(tasks[i])) and holdPublications(() => run(actions)) are Pi's own calls; host is ignored (one queue).
// stdout: per program, the labels in the order they were logged.
// usage: node publish_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const { enqueue, holdPublications } = await import(pathToFileURL(`${packages}chord/src/publish.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const program of JSON.parse(input)) {
	const log = [];
	const run = (actions) => {
		for (const action of actions) {
			if (action.log !== undefined) log.push(action.log);
			else if (action.enqueue !== undefined) enqueue(() => run(program.tasks[action.enqueue]));
			else holdPublications(() => run(action.hold.actions));
		}
	};
	run(program.top);
	results.push(log);
}
process.stdout.write(JSON.stringify(results));
