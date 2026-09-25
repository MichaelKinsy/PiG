// Fixture stand-in for upstream session loading, which skips malformed lines.
export function parseSessionEntries(content: string): unknown[] {
	const entries: unknown[] = [];
	for (const line of content.split("\n")) {
		try {
			entries.push(JSON.parse(line));
		} catch {
			// Skip malformed lines
		}
	}
	return entries;
}
