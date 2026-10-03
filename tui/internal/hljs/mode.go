package hljs

// Mode is a highlight.js mode or language object. JavaScript reads absent, undefined, null and false properties alike in every place the compiler and highlighter consult them, so each property keeps its value here and present records which own properties the object has: inherit and Object.assign copy exactly those.
type Mode struct {
	present modeKeys
	frozen  bool

	begin, end, match, illegal, lexemes, beforeMatch pattern

	className   string
	contains    []modeRef
	variants    []*Mode
	hasVariants bool
	starts      *Mode

	endsParent, endsWithParent, excludeBegin, excludeEnd, returnBegin, returnEnd, skip, endSameAsBegin bool

	relevance    float64
	hasRelevance bool

	keywords      keywordsValue
	beginKeywords string
	subLanguage   subLanguage
	onBegin       callback
	onEnd         callback

	caseInsensitive    bool
	name               string
	aliases            []string
	disableAutodetect  bool
	supersetOf         string
	classNameAliases   map[string]string
	compilerExtensions []compilerExtension

	// Properties the compiler and highlighter add.
	isCompiled        bool
	cachedVariants    []*Mode
	hasCachedVariants bool
	beforeBegin       callback
	keywordPatternRe  *jsRegExp
	beginRe           *jsRegExp
	endRe             *jsRegExp
	illegalRe         *jsRegExp
	terminatorEnd     string
	matcher           *resumableMultiRegex
	data              *responseData

	// rootFrame is the language object as the bottom of the mode stack; it is not a JavaScript property.
	rootFrame *frame
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

// pattern is a mode property that holds a RegExp, a pattern string or (for illegal) an array of them.
type pattern struct {
	kind patternKind
	src  string
	list []pattern
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
	switch key {
	case keyBegin:
		target.begin = source.begin
	case keyEnd:
		target.end = source.end
	case keyMatch:
		target.match = source.match
	case keyIllegal:
		target.illegal = source.illegal
	case keyLexemes:
		target.lexemes = source.lexemes
	case keyBeforeMatch:
		target.beforeMatch = source.beforeMatch
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
		target.keywords = source.keywords
	case keyBeginKeywords:
		target.beginKeywords = source.beginKeywords
	case keySubLanguage:
		target.subLanguage = source.subLanguage
	case keyOnBegin:
		target.onBegin = source.onBegin
	case keyOnEnd:
		target.onEnd = source.onEnd
	case keyCaseInsensitive:
		target.caseInsensitive = source.caseInsensitive
	case keyName:
		target.name = source.name
	case keyAliases:
		target.aliases = source.aliases
	case keyDisableAutodetect:
		target.disableAutodetect = source.disableAutodetect
	case keySupersetOf:
		target.supersetOf = source.supersetOf
	case keyClassNameAliases:
		target.classNameAliases = source.classNameAliases
	case keyCompilerExtensions:
		target.compilerExtensions = source.compilerExtensions
	case keyIsCompiled:
		target.isCompiled = source.isCompiled
	case keyCachedVariants:
		target.cachedVariants, target.hasCachedVariants = source.cachedVariants, source.hasCachedVariants
	case keyBeforeBegin:
		target.beforeBegin = source.beforeBegin
	case keyKeywordPatternRe:
		target.keywordPatternRe = source.keywordPatternRe
	case keyBeginRe:
		target.beginRe = source.beginRe
	case keyEndRe:
		target.endRe = source.endRe
	case keyIllegalRe:
		target.illegalRe = source.illegalRe
	case keyTerminatorEnd:
		target.terminatorEnd = source.terminatorEnd
	case keyMatcher:
		target.matcher = source.matcher
	case keyData:
		target.data = source.data
	}
}

// deleteKey is `delete mode[key]`.
func deleteKey(mode *Mode, key modeKeys) {
	copyKey(mode, &Mode{}, key)
	mode.present &^= key
}
