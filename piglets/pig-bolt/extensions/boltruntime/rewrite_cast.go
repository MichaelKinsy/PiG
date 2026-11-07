package boltruntime

// castImportMeta is whether the `import` at i begins `(import.meta as T).member` with a one-word type T and a word for member, where the `(` is no call's: the cast reads
// the member of import.meta as the plain access does.
func (a *analysis) castImportMeta(i int) bool {
	t := a.toks
	if i == 0 || i+7 >= len(t) || !a.isPunct(i-1, "(") || !a.isPunct(i+1, ".") || !a.isWord(i+2, "meta") || !a.isWord(i+3, "as") || t[i+4].kind != tkWord ||
		!a.isPunct(i+5, ")") || !a.isPunct(i+6, ".") || t[i+7].kind != tkWord {
		return false
	}
	if i == 1 {
		return true
	}
	before := t[i-2]
	switch before.kind {
	case tkWord:
		return beforeExpression[before.text]
	case tkPunct:
		return before.text != ")" && before.text != "]" && before.text != "}"
	}
	return false
}
