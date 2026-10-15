// Print `new URL(input, base)` as the OAuth callback server (callback-server.ts:80) reads it: origin, pathname and the search parameters grouped by name in first-seen order.
// stdin: [{input, base}]. Result: null when the constructor throws.
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map(({ input, base }) => {
			try {
				const url = new URL(input, base);
				const params = {};
				for (const [key, value] of url.searchParams) (params[key] ??= []).push(value);
				return { origin: url.origin, pathname: url.pathname, params: Object.entries(params) };
			} catch {
				return null;
			}
		}),
	),
);
