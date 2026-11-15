package host

// Ports packages/chord/src/host/read-declarations.ts, its pure checks (slice 14 §3, §4): what `self.queries`,
// `self.liveQuery`, `self.count` and `self.list` check, so an attempt and staging's recording `self` make the same ones
// (a reload whose new code declares a read wrongly keeps the plugin on its old code). Pi's checks of the builders' and
// the list's sources' shapes (`namedBuilders`' object of functions, `oneSource`'s `[[query, args]]`) check what Go's
// types state, and have no Go form.

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER: Pi's `Number.isSafeInteger` bound of a page size.
const maxSafeInteger = 1<<53 - 1

// firstOf checks the page size `{ first: n }` of a live query or a list: a positive integer (a bound is required).
func firstOf(label, declaration string, first int64) error {
	if first < 1 || first > maxSafeInteger {
		return declarationError("%s: self.%s: { first: n } is required, a positive integer", label, declaration)
	}
	return nil
}

// listName checks a list's name (non-empty, once per object: its bookmarks are kept by it) and remembers it in listed.
func listName(label, name string, listed map[string]struct{}) error {
	if name == "" {
		return declarationError("%s: self.list: a list has a name", label)
	}
	if _, twice := listed[name]; twice {
		return declarationError(`%s: list "%s" is declared twice`, label, name)
	}
	listed[name] = struct{}{}
	return nil
}

// namedQueryExpected is the refusal of what `self.liveQuery`, `self.count` or `self.list` (declaration) was given as
// its named query.
func namedQueryExpected(label, declaration string) error {
	return declarationError("%s: self.%s reads a named query; a table of this object is read with self.query", label, declaration)
}

// tableExpected is the refusal of a named query given to `self.query` as its table.
func tableExpected(label string) error {
	return declarationError("%s: self.query reads a table of this object; a named query is read with self.liveQuery", label)
}

// undeclaredTable is the refusal of a table given to `self.query` or `self.queries` (declaration) that the factory did
// not declare.
func undeclaredTable(label, declaration string) error {
	return declarationError("%s: self.%s: the table is not one this factory declared", label, declaration)
}
