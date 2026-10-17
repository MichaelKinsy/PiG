// Run Pi's createContextKey/withContextValue (packages/chord/src/context/index.ts, types stripped) over a script from stdin.
// Script: {descriptions: [string...]}. Answers {tokens: [{description, sameAsItself, sameAsEqualDescription, string}], layered: toString of the descriptions layered over BACKGROUND_CONTEXT in order}.
// usage: node context_key_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const { BACKGROUND_CONTEXT, createContextKey, withContextValue } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { descriptions } = JSON.parse(input);
const keys = descriptions.map((d) => createContextKey(d));
const again = descriptions.map((d) => createContextKey(d));
let ctx = BACKGROUND_CONTEXT;
keys.forEach((key, i) => (ctx = withContextValue(key, i, ctx)));
process.stdout.write(
	JSON.stringify({
		tokens: keys.map((key, i) => ({
			description: key.token.description ?? null,
			sameAsItself: key.token === key.token,
			sameAsEqualDescription: key.token === again[i].token,
			valueUnderOtherKey: withContextValue(key, 1, BACKGROUND_CONTEXT).value(again[i]) ?? null,
		})),
		layered: String(ctx),
	}),
);
