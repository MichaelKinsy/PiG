package hljs

import "slices"

// Language-definition callbacks, ported by the names automation/gen/generate-highlight-grammars.mjs assigns to their pinned sources.
var callbacks = map[string]callback{
	// END_SAME_AS_BEGIN (lib/core.js): the end must repeat the begin's first group.
	"endSameAsBegin.begin": func(m *enhancedMatch, resp *response) {
		group, ok := m.group(1)
		resp.data.beginMatch, resp.data.hasBeginMatch = unitsToString(group), ok
	},
	"endSameAsBegin.end": func(m *enhancedMatch, resp *response) {
		group, ok := m.group(1)
		if resp.data.hasBeginMatch != ok || resp.data.beginMatch != unitsToString(group) {
			resp.ignoreMatch()
		}
	},
	// SHEBANG (lib/core.js): only at the start of the code.
	"shebang.begin": func(m *enhancedMatch, resp *response) {
		if m.index != 0 {
			resp.ignoreMatch()
		}
	},
	// XML_TAG.isTrulyOpeningTag (lib/languages/javascript.js, typescript.js): a JSX tag, not a type argument.
	"javascript.isTrulyOpeningTag": func(m *enhancedMatch, resp *response) {
		lexeme := m.lexeme()
		afterMatchIndex := len(lexeme) + m.index
		if afterMatchIndex >= len(m.input) {
			return
		}
		switch m.input[afterMatchIndex] {
		case '<':
			resp.ignoreMatch()
		case '>':
			if !hasClosingTag(m, afterMatchIndex) {
				resp.ignoreMatch()
			}
		}
	},
	// SYMBOLS (lib/languages/mathematica.js): a builtin symbol only when SYSTEM_SYMBOLS lists it.
	"mathematica.systemSymbol": func(m *enhancedMatch, resp *response) {
		loadTables()
		if !systemSymbolSet[unitsToString(m.lexeme())] {
			resp.ignoreMatch()
		}
	},
}

// hasClosingTag (lib/languages/javascript.js): match.input.indexOf("</" + match[0].slice(1), after) !== -1.
func hasClosingTag(m *enhancedMatch, after int) bool {
	lexeme := m.lexeme()
	tag := append([]rune{'<', '/'}, lexeme[min(1, len(lexeme)):]...)
	from := min(after, len(m.input))
	for i := from; i+len(tag) <= len(m.input); i++ {
		if slices.Equal(m.input[i:i+len(tag)], tag) {
			return true
		}
	}
	return false
}

func callbackByName(value any) callback {
	object, _ := value.(map[string]any)
	name, _ := object["fn"].(string)
	cb, ok := callbacks[name]
	if !ok {
		panic("hljs: unknown callback " + name)
	}
	return cb
}

var compilerExtensions = map[string]compilerExtension{
	// The beforeMatch compiler extension of lib/languages/r.js: the match must be preceded by beforeMatch.
	"r.beforeMatch": func(mode, _ *Mode) {
		if !mode.beforeMatch.truthy() {
			return
		}
		if mode.starts != nil {
			throw("beforeMatch cannot be used with starts")
		}
		originalMode := &Mode{}
		assign(originalMode, mode)
		for key := modeKeys(1); key < keyLimit; key <<= 1 {
			if mode.present&key != 0 {
				deleteKey(mode, key)
			}
		}
		beforeMatch, _ := originalMode.beforeMatch.source()
		begin, _ := originalMode.begin.source()
		mode.begin = stringPattern(beforeMatch + "(?=" + begin + ")")
		mode.present |= keyBegin
		originalMode.endsParent = true
		originalMode.present |= keyEndsParent
		mode.starts = &Mode{present: keyRelevance | keyContains, hasRelevance: true, contains: []modeRef{{mode: originalMode}}}
		mode.present |= keyStarts
		mode.relevance, mode.hasRelevance = 0, true
		mode.present |= keyRelevance
		deleteKey(originalMode, keyBeforeMatch)
	},
}
