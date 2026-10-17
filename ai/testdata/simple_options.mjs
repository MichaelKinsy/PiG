// Run Pi's simple-options.ts (imported as-is, types stripped) over probes from stdin.
// usage: node simple_options.mjs <simple-options.ts>
// {op: "budget"|"adjust"|"room"|"clamp", level, custom, base, modelMax, ceiling, budget}. Results: numbers or objects.
import { pathToFileURL } from "node:url";
const options = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			switch (p.op) {
				case "budget":
					return options.thinkingBudgetForLevel(p.level, p.custom ?? undefined);
				case "adjust":
					return options.adjustMaxTokensForThinking(p.base ?? undefined, p.modelMax, p.level, p.custom ?? undefined);
				case "room":
					return options.clampThinkingBudgetToAnswerRoom(p.budget, p.ceiling);
				default:
					return options.clampReasoning(p.level);
			}
		}),
	),
);
