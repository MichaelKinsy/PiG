// A parser for the TOML subset corpus.toml uses: comments, [table], [[array of tables]], and `key = value` with strings,
// integers, booleans, arrays and inline tables of those. It throws on anything else, so the manifest cannot drift into
// syntax the Go tools would read differently.
export function parseToml(text) {
	const root = {}
	let table = root
	const lines = text.split("\n")
	for (let n = 0; n < lines.length; n++) {
		const line = stripComment(lines[n]).trim()
		if (!line) continue
		let m
		if ((m = /^\[\[([A-Za-z0-9_.-]+)\]\]$/.exec(line))) {
			const path = m[1].split(".")
			const parent = path.slice(0, -1).reduce((t, k) => (t[k] ??= {}), root)
			const list = (parent[path.at(-1)] ??= [])
			table = {}
			list.push(table)
		} else if ((m = /^\[([A-Za-z0-9_.-]+)\]$/.exec(line))) {
			table = m[1].split(".").reduce((t, k) => (t[k] ??= {}), root)
		} else if ((m = /^([A-Za-z0-9_-]+|"[^"]*")\s*=\s*(.*)$/.exec(line))) {
			let rest = m[2]
			// An array may continue over several lines.
			while (depth(rest) > 0 && n + 1 < lines.length) rest += " " + stripComment(lines[++n]).trim()
			const key = m[1].replace(/^"|"$/g, "")
			const [value, tail] = parseValue(rest.trim(), n + 1)
			if (tail.trim()) throw new Error(`toml: line ${n + 1}: trailing text ${JSON.stringify(tail)}`)
			table[key] = value
		} else throw new Error(`toml: line ${n + 1}: cannot parse ${JSON.stringify(line)}`)
	}
	return root
}

function stripComment(line) {
	let inString = false
	for (let i = 0; i < line.length; i++) {
		const c = line[i]
		if (c === "\\" && inString) i++
		else if (c === '"') inString = !inString
		else if (c === "#" && !inString) return line.slice(0, i)
	}
	return line
}
function depth(s) {
	let d = 0, inString = false
	for (let i = 0; i < s.length; i++) {
		const c = s[i]
		if (c === "\\" && inString) i++
		else if (c === '"') inString = !inString
		else if (!inString && (c === "[" || c === "{")) d++
		else if (!inString && (c === "]" || c === "}")) d--
	}
	return d
}
function parseValue(s, line) {
	if (s[0] === '"') {
		let i = 1
		while (i < s.length && s[i] !== '"') i += s[i] === "\\" ? 2 : 1
		return [JSON.parse(s.slice(0, i + 1)), s.slice(i + 1)]
	}
	if (s[0] === "[") {
		const out = []
		let rest = s.slice(1).trim()
		while (rest[0] !== "]") {
			const [v, tail] = parseValue(rest, line)
			out.push(v)
			rest = tail.trim()
			if (rest[0] === ",") rest = rest.slice(1).trim()
			else if (rest[0] !== "]") throw new Error(`toml: line ${line}: expected , or ]`)
		}
		return [out, rest.slice(1)]
	}
	if (s[0] === "{") {
		const out = {}
		let rest = s.slice(1).trim()
		while (rest[0] !== "}") {
			const m = /^([A-Za-z0-9_-]+|"[^"]*")\s*=\s*/.exec(rest)
			if (!m) throw new Error(`toml: line ${line}: bad inline table`)
			const [v, tail] = parseValue(rest.slice(m[0].length), line)
			out[m[1].replace(/^"|"$/g, "")] = v
			rest = tail.trim()
			if (rest[0] === ",") rest = rest.slice(1).trim()
		}
		return [out, rest.slice(1)]
	}
	let m
	if ((m = /^(true|false)\b/.exec(s))) return [m[1] === "true", s.slice(m[0].length)]
	if ((m = /^-?\d[\d_]*(\.\d+)?/.exec(s))) return [Number(m[0].replace(/_/g, "")), s.slice(m[0].length)]
	throw new Error(`toml: line ${line}: cannot parse value ${JSON.stringify(s)}`)
}
