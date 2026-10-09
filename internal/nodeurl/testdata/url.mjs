// Run Node's `new URL(value)` over inputs from stdin: [string] -> [{ok, protocol, hostname, search}], ok false where the constructor throws.
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((value) => {
			try {
				const url = new URL(value);
				return { ok: true, protocol: url.protocol, hostname: url.hostname, search: url.search };
			} catch {
				return { ok: false, protocol: "", hostname: "", search: "" };
			}
		}),
	),
);
