// A `node --test` reporter that prints one JSON line per finished case or suite: its full title (suite titles, then the case's,
// joined by " > "), whether it passed, was skipped or is a todo, and the failure's message; and a last line with the summary.
// oracle.RunNodeCases reads them.
export default async function* (source) {
	const stack = [];
	for await (const event of source) {
		const { type, data } = event;
		if (type === "test:start") {
			stack.push(data.name);
		} else if (type === "test:pass" || type === "test:fail") {
			const title = stack.join(" > ");
			stack.pop();
			const error = data.details?.error;
			yield `${JSON.stringify({
				title,
				nesting: data.nesting,
				ok: type === "test:pass",
				skip: data.skip !== undefined && data.skip !== false,
				todo: data.todo !== undefined && data.todo !== false,
				error: error === undefined ? undefined : String(error.cause?.stack ?? error.stack ?? error.message ?? error),
			})}\n`;
		}
	}
}
