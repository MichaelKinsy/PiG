// Run Pi's getResponsesToolSupport and convertResponsesTools (api/openai-responses-shared.ts), splitErrorToolResultImages (api/transform-messages.ts) and
// ToolCallArgumentsStream.finish (utils/streaming-json.ts), imported as-is with types stripped, over cases from stdin.
//   {kind: "toolSupport", options} | {kind: "tools", tools, supportsStrictMode} | {kind: "split", message} | {kind: "finish", pieces, replace}
// Answers {ok: <JSON.stringify text, or null for undefined>} or {err: message}.
// usage: node strict_sampling_exports_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai deps)
import module from "node:module";
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		if (!specifier.startsWith(".") && !specifier.startsWith("node:") && !specifier.startsWith("file:") && !specifier.startsWith("/"))
			return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const load = (path) => import(pathToFileURL(`${packages}ai/src/${path}`).href);
const shared = await load("api/openai-responses-shared.ts");
const transform = await load("api/transform-messages.ts");
const streaming = await load("utils/streaming-json.ts");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const answer = (run) => {
	try {
		const value = run();
		return { ok: value === undefined ? null : JSON.stringify(value) };
	} catch (error) {
		return { err: error instanceof Error ? error.message : String(error) };
	}
};
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((c) => {
			switch (c.kind) {
				case "toolSupport":
					return answer(() => shared.getResponsesToolSupport(c.options));
				case "tools":
					return answer(() => shared.convertResponsesTools(c.tools, { supportsStrictMode: c.supportsStrictMode }));
				case "split":
					return answer(() => transform.splitErrorToolResultImages(c.message));
				default:
					return answer(() => {
						const stream = new streaming.ToolCallArgumentsStream();
						for (const piece of c.pieces) stream.append(piece);
						if (c.replace !== undefined && c.replace !== null) stream.replace(c.replace);
						return stream.finish();
					});
			}
		}),
	),
);
