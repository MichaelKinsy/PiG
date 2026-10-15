package hljs

// Mode is a highlight.js mode or language object. JavaScript reads absent, undefined, null and false properties alike in every place the compiler and highlighter consult them, so each property keeps its value here and present records which own properties the object has: inherit and Object.assign copy exactly those.
type Mode struct {
	present modeKeys

	// language holds the properties only a language object sets; it is nil on every other mode, which reads them as absent.
	language *languageProps

	begin, end pattern

	// rarer holds the properties most modes leave unset; it is nil on a mode that sets none of them, which reads them as absent.
	rarer *rareProps

	className string
	contains  []modeRef
	variants  []*Mode
	starts    *Mode

	endsParent, endsWithParent, excludeBegin, excludeEnd, returnBegin, returnEnd, skip, endSameAsBegin bool

	// The remaining flags sit together so the struct carries no padding after each of them; a registry holds thousands of modes.
	frozen, hasVariants, hasRelevance, caseInsensitive, disableAutodetect, isCompiled, hasCachedVariants bool

	relevance float64

	// compiled holds the properties the compiler and highlighter add; it is nil until one is set, and a mode without it reads them as absent.
	compiled *compiledProps
}

// rareProps are the mode properties most grammar modes leave unset (of about 4,000 bundled modes, about 1,000 set one). They sit outside Mode so a registry does not carry their space on every mode.
type rareProps struct {
	match, illegal, lexemes, beforeMatch pattern

	keywords      keywordsValue
	beginKeywords string
	subLanguage   subLanguage
	onBegin       callback
	onEnd         callback
}

// rareKeys are the keys whose values live in rareProps.
const rareKeys = keyMatch | keyIllegal | keyLexemes | keyBeforeMatch | keyKeywords | keyBeginKeywords | keySubLanguage | keyOnBegin | keyOnEnd

// noRareProps is what a mode without rare properties reads.
var noRareProps rareProps

// rare returns the mode's rare properties for reading; a mode without them reads the zero values.
func (m *Mode) rare() *rareProps {
	if m.rarer == nil {
		return &noRareProps
	}
	return m.rarer
}

// setRare returns the mode's rare properties for writing, adding them on first use.
func (m *Mode) setRare() *rareProps {
	if m.rarer == nil {
		m.rarer = new(rareProps)
	}
	return m.rarer
}

// compiledProps are the properties the compiler and highlighter add to a mode. Only the modes of a highlighted language get them, so they sit outside Mode.
type compiledProps struct {
	cachedVariants   []*Mode
	beforeBegin      callback
	keywordPatternRe *jsRegExp
	beginRe          *jsRegExp
	endRe            *jsRegExp
	illegalRe        *jsRegExp
	terminatorEnd    string
	matcher          *resumableMultiRegex
	data             *responseData
}

// languageProps are the properties of a language object. A registry holds a few hundred languages among thousands of modes, so they sit outside Mode.
type languageProps struct {
	name               string
	aliases            []string
	supersetOf         string
	classNameAliases   map[string]string
	compilerExtensions []compilerExtension

	// sources are the patterns compileLanguage compiled for the language, and reach the greatest reach of any of them.
	sources   []string
	reach     reach
	reachDone bool

	// rootFrame is the language object as the bottom of the mode stack; it is not a JavaScript property.
	rootFrame *frame
}

// noCompiledProps is what a mode without compiled properties reads.
var noCompiledProps compiledProps

// comp returns the mode's compiled properties for reading; a mode without them reads the zero values.
func (m *Mode) comp() *compiledProps {
	if m.compiled == nil {
		return &noCompiledProps
	}
	return m.compiled
}

// setComp returns the mode's compiled properties for writing, adding them on first use.
func (m *Mode) setComp() *compiledProps {
	if m.compiled == nil {
		m.compiled = new(compiledProps)
	}
	return m.compiled
}

// noLanguageProps is what a mode without language properties reads.
var noLanguageProps languageProps

// lang returns the mode's language properties for reading; a mode without them reads the zero values.
func (m *Mode) lang() *languageProps {
	if m.language == nil {
		return &noLanguageProps
	}
	return m.language
}

// setLang returns the mode's language properties for writing, adding them on first use.
func (m *Mode) setLang() *languageProps {
	if m.language == nil {
		m.language = new(languageProps)
	}
	return m.language
}

type modeKeys uint64

const (
	keyBegin modeKeys = 1 << iota
	keyEnd
	keyMatch
	keyIllegal
	keyLexemes
	keyBeforeMatch
	keyClassName
	keyContains
	keyVariants
	keyStarts
	keyEndsParent
	keyEndsWithParent
	keyExcludeBegin
	keyExcludeEnd
	keyReturnBegin
	keyReturnEnd
	keySkip
	keyEndSameAsBegin
	keyRelevance
	keyKeywords
	keyBeginKeywords
	keySubLanguage
	keyOnBegin
	keyOnEnd
	keyCaseInsensitive
	keyName
	keyAliases
	keyDisableAutodetect
	keySupersetOf
	keyClassNameAliases
	keyCompilerExtensions
	keyIsCompiled
	keyCachedVariants
	keyBeforeBegin
	keyKeywordPatternRe
	keyBeginRe
	keyEndRe
	keyIllegalRe
	keyTerminatorEnd
	keyMatcher
	keyData
	keyLimit
)

// modeRef is an entry of contains: a mode, or the string "self".
type modeRef struct {
	mode *Mode
	self bool
}

type patternKind uint8

const (
	patternNone patternKind = iota // undefined, null or false
	patternString
	patternRegExp
	patternList
)

// pattern is a mode property that holds a RegExp, a pattern string or (for illegal) an array of them. Only an illegal array has a list, so the list sits behind a pointer to keep the six patterns of every mode small.
type pattern struct {
	kind patternKind
	src  string
	list *[]pattern
}

// items is the array of an illegal array pattern, and nil for any other pattern.
func (p pattern) items() []pattern {
	if p.list == nil {
		return nil
	}
	return *p.list
}

func stringPattern(source string) pattern { return pattern{kind: patternString, src: source} }

func regExpPattern(source string) pattern { return pattern{kind: patternRegExp, src: source} }

// truthy is JavaScript truthiness: an empty pattern string is false, a RegExp and an array are true.
func (p pattern) truthy() bool {
	switch p.kind {
	case patternString:
		return p.src != ""
	case patternRegExp, patternList:
		return true
	}
	return false
}

// source is highlight.js source(re): null for a falsy value, the string itself, or RegExp.source.
func (p pattern) source() (string, bool) {
	if !p.truthy() || p.kind == patternList {
		return "", false
	}
	return p.src, true
}

type keywordsKind uint8

const (
	keywordsKindNone keywordsKind = iota
	keywordsKindString
	keywordsKindList
	keywordsKindObject
	keywordsKindCompiled
)

// keywordsValue is mode.keywords: a keyword string, an array of keywords, a keywords object (shared by identity) or, after compilation, the keyword dictionary.
type keywordsValue struct {
	kind     keywordsKind
	text     string
	list     []string
	object   *keywordsObject
	compiled map[string]keywordData
}

func (k keywordsValue) truthy() bool {
	switch k.kind {
	case keywordsKindString:
		return k.text != ""
	case keywordsKindList, keywordsKindObject, keywordsKindCompiled:
		return true
	}
	return false
}

// keywordsObject is a raw keywords object. compileMode deletes its $pattern, which every mode sharing the object observes.
type keywordsObject struct {
	pattern    pattern
	hasPattern bool
	classes    []keywordClass
}

// keywordClass is one class name of a keywords object, in property order, with a string or array value.
type keywordClass struct {
	name   string
	text   string
	list   []string
	isList bool
}

type keywordData struct {
	kind      string
	relevance float64
}

// subLanguage is mode.subLanguage: unset (null or undefined), one language name, or a list for auto-detection.
type subLanguage struct {
	set    bool
	isList bool
	name   string
	list   []string
}

// truthy is JavaScript truthiness of mode.subLanguage: an empty name is false, a list is true.
func (s subLanguage) truthy() bool { return s.set && (s.isList || s.name != "") }

// responseData is a mode's data object; the only property highlight.js callbacks keep in it is END_SAME_AS_BEGIN's _beginMatch.
type responseData struct {
	beginMatch    string
	hasBeginMatch bool
}

type callback func(match *enhancedMatch, response *response)

type compilerExtension func(mode, parent *Mode)

// inherit is highlight.js inherit(original, ...objects): a new unfrozen object with every own property of original, then of each object in turn.
func inherit(original *Mode, overrides ...*Mode) *Mode {
	result := &Mode{}
	assign(result, original)
	for _, override := range overrides {
		assign(result, override)
	}
	return result
}

// assign is Object.assign(target, source) over mode properties.
func assign(target, source *Mode) {
	for key := modeKeys(1); key < keyLimit; key <<= 1 {
		if source.present&key != 0 {
			copyKey(target, source, key)
		}
	}
}

func copyKey(target, source *Mode, key modeKeys) {
	target.present |= key
	if key&rareKeys != 0 && source.rarer == nil && target.rarer == nil {
		return // both read the zero value
	}
	switch key {
	case keyBegin:
		target.begin = source.begin
	case keyEnd:
		target.end = source.end
	case keyMatch:
		target.setRare().match = source.rare().match
	case keyIllegal:
		target.setRare().illegal = source.rare().illegal
	case keyLexemes:
		target.setRare().lexemes = source.rare().lexemes
	case keyBeforeMatch:
		target.setRare().beforeMatch = source.rare().beforeMatch
	case keyClassName:
		target.className = source.className
	case keyContains:
		target.contains = source.contains
	case keyVariants:
		target.variants, target.hasVariants = source.variants, source.hasVariants
	case keyStarts:
		target.starts = source.starts
	case keyEndsParent:
		target.endsParent = source.endsParent
	case keyEndsWithParent:
		target.endsWithParent = source.endsWithParent
	case keyExcludeBegin:
		target.excludeBegin = source.excludeBegin
	case keyExcludeEnd:
		target.excludeEnd = source.excludeEnd
	case keyReturnBegin:
		target.returnBegin = source.returnBegin
	case keyReturnEnd:
		target.returnEnd = source.returnEnd
	case keySkip:
		target.skip = source.skip
	case keyEndSameAsBegin:
		target.endSameAsBegin = source.endSameAsBegin
	case keyRelevance:
		target.relevance, target.hasRelevance = source.relevance, source.hasRelevance
	case keyKeywords:
		target.setRare().keywords = source.rare().keywords
	case keyBeginKeywords:
		target.setRare().beginKeywords = source.rare().beginKeywords
	case keySubLanguage:
		target.setRare().subLanguage = source.rare().subLanguage
	case keyOnBegin:
		target.setRare().onBegin = source.rare().onBegin
	case keyOnEnd:
		target.setRare().onEnd = source.rare().onEnd
	case keyCaseInsensitive:
		target.caseInsensitive = source.caseInsensitive
	case keyName:
		target.setLang().name = source.lang().name
	case keyAliases:
		target.setLang().aliases = source.lang().aliases
	case keyDisableAutodetect:
		target.disableAutodetect = source.disableAutodetect
	case keySupersetOf:
		target.setLang().supersetOf = source.lang().supersetOf
	case keyClassNameAliases:
		target.setLang().classNameAliases = source.lang().classNameAliases
	case keyCompilerExtensions:
		target.setLang().compilerExtensions = source.lang().compilerExtensions
	case keyIsCompiled:
		target.isCompiled = source.isCompiled
	case keyCachedVariants:
		target.setComp().cachedVariants, target.hasCachedVariants = source.comp().cachedVariants, source.hasCachedVariants
	case keyBeforeBegin:
		target.setComp().beforeBegin = source.comp().beforeBegin
	case keyKeywordPatternRe:
		target.setComp().keywordPatternRe = source.comp().keywordPatternRe
	case keyBeginRe:
		target.setComp().beginRe = source.comp().beginRe
	case keyEndRe:
		target.setComp().endRe = source.comp().endRe
	case keyIllegalRe:
		target.setComp().illegalRe = source.comp().illegalRe
	case keyTerminatorEnd:
		target.setComp().terminatorEnd = source.comp().terminatorEnd
	case keyMatcher:
		target.setComp().matcher = source.comp().matcher
	case keyData:
		target.setComp().data = source.comp().data
	}
}

// deleteKey is `delete mode[key]`.
func deleteKey(mode *Mode, key modeKeys) {
	copyKey(mode, &Mode{}, key)
	mode.present &^= key
}
