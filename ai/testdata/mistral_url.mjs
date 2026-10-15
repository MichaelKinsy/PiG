// Run the URL expression of Pi's mistral-conversations.ts requestMistralStream (inline, so copied) over base URLs from stdin:
//   const baseUrl = new URL(model.baseUrl); baseUrl.pathname = `${baseUrl.pathname.replace(/\/+$/u, "")}/`; new URL("v1/chat/completions", baseUrl).toString()
// Result: the string, or null when a URL constructor throws.
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((modelBaseUrl) => {
			try {
				const baseUrl = new URL(modelBaseUrl);
				baseUrl.pathname = `${baseUrl.pathname.replace(/\/+$/u, "")}/`;
				return new URL("v1/chat/completions", baseUrl).toString();
			} catch (error) {
				if (!(error instanceof TypeError)) throw error;
				return null;
			}
		}),
	),
);
