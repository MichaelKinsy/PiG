// Run the URL expressions of Pi's pi-messages.ts, cloudflare-workers-ai-system-one.ts, typesafe-system-one.ts and openai-decisions.ts over base URLs from stdin (the expressions are inline in
// closures, so they are copied). Results are the string, or null when the URL constructor throws.
// {op:"service", baseUrl, path}: new URL(path, `${baseUrl.replace(/\/+$/u, "")}/`).toString() (run, systemone, decisions)
// {op:"messages", baseUrl, debug}: const url = new URL(`${baseUrl.replace(/\/+$/u, "")}/messages`); if (debug) url.searchParams.set("debug", "1"); url.toString()
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			try {
				if (p.op === "service") return new URL(p.path, `${p.baseUrl.replace(/\/+$/u, "")}/`).toString();
				const url = new URL(`${p.baseUrl.replace(/\/+$/u, "")}/messages`);
				if (p.debug) url.searchParams.set("debug", "1");
				return url.toString();
			} catch (error) {
				if (!(error instanceof TypeError)) throw error;
				return null;
			}
		}),
	),
);
