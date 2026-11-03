// Preloaded (node --import) before Pi's packages/ai/src/cli.ts runs as-is with types stripped. cli.ts takes its providers from
// ./providers/all.ts, which needs the generated model data; the hook answers that one import with the stub providers of
// ai_cli_oracle_providers.mjs (PI_AI_CLI_PROVIDERS names the file) and leaves every other import to node.
import module from "node:module";
import { pathToFileURL } from "node:url";

module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier === "./providers/all.ts") {
			return { url: pathToFileURL(process.env.PI_AI_CLI_PROVIDERS).href, shortCircuit: true };
		}
		return next(specifier, context);
	},
});
