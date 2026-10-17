// Print Node's own `new URL(value)` fields for each input.
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((value) => {
	try {
		const u = new URL(value);
		const fields = { protocol: u.protocol, username: u.username, password: u.password, hostname: u.hostname, pathname: u.pathname, hash: u.hash, origin: u.origin, href: u.href };
		const withSearch = new URL(value);
		withSearch.search = "?a b'c\"d<e>#f&g=h";
		fields.searched = withSearch.toString();
		u.pathname = "/new path/x%2Fy?z";
		return { ...fields, replaced: u.toString() };
	} catch { return null; }
})));
