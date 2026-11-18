package hljs

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsarray"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Ports highlight.js 10.7.3 lib/core.js (the compiler, the highlighter and the HTML emitter), the highlighter Pi's packages/coding-agent/src/utils/syntax-highlight.ts drives.

// hljsError is a JavaScript exception raised inside the highlighter. Only these are caught where highlight.js catches; any other panic is a defect of the port.
type hljsError struct{ message string }

func throw(message string) { panic(&hljsError{message: message}) }

// response is highlight.js Response: the callback view of a mode's data and the ignore flag.
type response struct {
	data           *responseData
	isMatchIgnored bool
}

func (r *response) ignoreMatch() { r.isMatchIgnored = true }

func newRuleResponse(mode *Mode) *response {
	return &response{data: ruleData(mode)}
}

// ruleData is mode.data as new Response(mode) leaves it: an object the mode gets when it has none.
func ruleData(mode *Mode) *responseData {
	compiled := mode.setComp()
	if compiled.data == nil {
		compiled.data = &responseData{}
		mode.present |= keyData
	}
	return compiled.data
}

// frame is `Object.create(mode, { parent: { value: top } })`: a stack entry that reads through to its mode. The language itself is the bottom of the stack, so its frame is unique per language and writes reach the language object.
type frame struct {
	mode     *Mode
	parent   *frame
	data     *responseData
	language bool
}

func (f *frame) response() *response {
	if f.language {
		return newRuleResponse(f.mode)
	}
	if f.data == nil {
		if data := f.mode.comp().data; data != nil {
			return &response{data: data}
		}
		f.data = &responseData{}
	}
	return &response{data: f.data}
}

// enhancedMatch is a MultiRegex exec result after match.splice(0, i) and Object.assign(match, matchData). offset is the number, in the expression that matched, of the group that wraps the matched rule; the rule's own groups follow it.
type enhancedMatch struct {
	input    []rune
	index    int
	match    jsMatch
	offset   int
	opts     *matchOptions
	position int
}

// group is match[k]: the code units of a participating group, ok false for undefined. match[0] is the text of the rule that matched, which is the whole match. A later group is one of the rule's own, which the matcher's expression holds only for a rule whose callbacks read them (matchOptions.groups).
func (m *enhancedMatch) group(k int) ([]rune, bool) {
	if k == 0 {
		return m.input[m.match.index:m.match.end], true
	}
	if m.opts != nil && !m.opts.groups {
		panic("hljs: a group of a rule whose groups its matcher leaves out")
	}
	start, end, ok := m.match.span(m.offset + k)
	if !ok {
		return nil, false
	}
	return m.input[start:end], true
}

func (m *enhancedMatch) lexeme() []rune {
	text, _ := m.group(0)
	return text
}

func (m *enhancedMatch) rule() *Mode {
	if m.opts == nil {
		return nil
	}
	return m.opts.rule
}

func (m *enhancedMatch) matchType() string {
	if m.opts == nil {
		return ""
	}
	return m.opts.typ
}

// matchOptions is the opts object a rule is registered with. It is shared by every MultiRegex built from the same rules, and each addRule renumbers its position. groups is set for a rule whose match a callback of its mode reads (on:begin, on:end, beforeBegin): a matcher's expression then holds the rule's own groups.
type matchOptions struct {
	rule     *Mode
	typ      string
	position int
	groups   bool
}

// multiRegexRule is a rule of a MultiRegex. group is the number of the group that wraps it in the matcher's expression, where highlight.js keeps matchIndexes: the groups before it are those of the rules before it that the expression holds.
type multiRegexRule struct {
	re    pattern
	opts  *matchOptions
	group int
}

type multiRegex struct {
	regexes   []multiRegexRule
	position  int
	matcherRe *jsRegExp
	empty     bool
	lastIndex int
	dispatch  *branchDispatch
	// emptyRule is set for a matcher of one rule that matches the empty string everywhere (matchesEmptyEverywhere): the end of a mode that has none and contains nothing.
	emptyRule bool
}

// matchesEmptyEverywhere is the pattern highlight.js gives a mode without a begin or without an end. One of \B and \b holds at every position, the end of the text included, so it matches the empty string wherever a search for it starts.
const matchesEmptyEverywhere = `\B|\b`

// emptyMatch is the match of matchesEmptyEverywhere at index.
func emptyMatch(index int) jsMatch { return jsMatch{index: index, end: index} }

func newMultiRegex() *multiRegex { return &multiRegex{} }

func (m *multiRegex) addRule(re pattern, opts *matchOptions) {
	opts.position = m.position
	m.position++
	m.regexes = append(m.regexes, multiRegexRule{re: re, opts: opts})
}

func (m *multiRegex) compile(language *Mode) {
	if len(m.regexes) == 0 {
		m.empty = true
	}
	source, groups := joinRules(m.regexes)
	for i := range m.regexes {
		m.regexes[i].group = groups[i]
	}
	// The joined terminators are alternatives of patterns compileMode recorded, so they add no reach, and recording them would make a language's reach depend on the matchers its first texts happened to compile.
	m.matcherRe = compileLangRe(language, stringPattern(source))
	if len(m.regexes) == 1 {
		only, _ := m.regexes[0].re.source()
		m.emptyRule = dispatchEnabled && only == matchesEmptyEverywhere
	}
	if dispatchEnabled {
		m.dispatch = newBranchDispatch(m.regexes, language.caseInsensitive)
	}
}

// exec is MultiRegex.exec. The match is written to result, which the caller owns, and returned; nil is no match.
func (m *multiRegex) exec(s []rune, result *enhancedMatch) *enhancedMatch {
	if m.empty {
		return nil
	}
	var match jsMatch
	rule, group, ok := -1, 0, false
	switch {
	case m.dispatch != nil:
		match, rule, group, ok = m.dispatch.exec(m.matcherRe, s, m.lastIndex)
	case m.emptyRule:
		if m.lastIndex <= len(s) {
			match, rule, group, ok = emptyMatch(m.lastIndex), 0, m.regexes[0].group, true
		}
	default:
		if match, ok = m.matcherRe.exec(s, m.lastIndex); ok {
			rule, group = matchedRule(match, m.regexes)
		}
	}
	if !ok {
		return nil
	}
	*result = enhancedMatch{input: s, index: match.index, match: match, offset: group}
	if rule >= 0 {
		result.opts = m.regexes[rule].opts
		result.position = result.opts.position
	}
	return result
}

// matchedRule is the rule of a matcher's expression that a match of it matched, and the number of the rule's group: the first rule whose group took part, as highlight.js finds the first defined group. A match of an expression of one rule is that rule's.
func matchedRule(match jsMatch, rules []multiRegexRule) (rule, group int) {
	if len(rules) == 1 {
		return 0, rules[0].group
	}
	for i := range rules {
		if _, _, ok := match.span(rules[i].group); ok {
			return i, rules[i].group
		}
	}
	return -1, 0
}

// resumableMultiRegex is highlight.js ResumableMultiRegex: it resumes after an ignored rule at the same position.
type resumableMultiRegex struct {
	rules        []multiRegexRule
	multiRegexes map[int]*multiRegex
	count        int
	lastIndex    int
	regexIndex   int
	language     *Mode
}

func (r *resumableMultiRegex) getMatcher(index int) *multiRegex {
	if m := r.multiRegexes[index]; m != nil {
		return m
	}
	matcher := newMultiRegex()
	if index < len(r.rules) {
		for _, rule := range r.rules[index:] {
			matcher.addRule(rule.re, rule.opts)
		}
	}
	matcher.compile(r.language)
	r.multiRegexes[index] = matcher
	return matcher
}

func (r *resumableMultiRegex) resumingScanAtSamePosition() bool { return r.regexIndex != 0 }

func (r *resumableMultiRegex) considerAll() { r.regexIndex = 0 }

func (r *resumableMultiRegex) addRule(re pattern, opts *matchOptions) {
	r.rules = append(r.rules, multiRegexRule{re: re, opts: opts})
	if opts.typ == "begin" {
		r.count++
	}
}

// exec is ResumableMultiRegex.exec, into a match the caller owns (multiRegex.exec).
func (r *resumableMultiRegex) exec(s []rune, into *enhancedMatch) *enhancedMatch {
	m := r.getMatcher(r.regexIndex)
	m.lastIndex = r.lastIndex
	result := m.exec(s, into)
	if r.resumingScanAtSamePosition() {
		if result == nil || result.index != r.lastIndex {
			m2 := r.getMatcher(0)
			m2.lastIndex = r.lastIndex + 1
			result = m2.exec(s, into)
		}
	}
	if result != nil {
		r.regexIndex += result.position + 1
		if r.regexIndex == r.count {
			r.considerAll()
		}
	}
	return result
}

// countMatchGroups is the number of capturing groups in a rule's begin pattern.
func countMatchGroups(re pattern) int {
	source, _ := re.source()
	t := &regexpTranslator{src: toUnits(source)}
	t.scanGroups()
	return t.groupCount
}

// backrefRE is highlight.js BACKREF_RE. JavaScript's . excludes the line terminators.
var backrefRE = lazyregexp.New(`\[(?:[^\\\]]|\\[^\n\r\x{2028}\x{2029}])*\]|\(\??|\\([1-9][0-9]*)|\\[^\n\r\x{2028}\x{2029}]`)

// ruleGroupsLeftOut is false only in tests that compare a matcher with the expression that holds every group; it is read when a matcher compiles.
var ruleGroupsLeftOut = true

// joinRules is highlight.js join(regexps, "|") over the rules of a matcher: each pattern in its own group, with backreferences renumbered. groups is the number of each rule's group. The groups inside a pattern are left out of the expression (written as non-capturing) unless the pattern refers to a group (a backreference, a named group) or a callback reads the rule's match (matchOptions.groups): the highlighter reads which rule matched and the text it matched, and an expression of dozens of rules otherwise records the groups of all of them for every match. What matches is the same: a group that nothing refers to changes no match.
func joinRules(rules []multiRegexRule) (source string, groups []int) {
	numCaptures := 0
	parts := make([]string, len(rules))
	groups = make([]int, len(rules))
	for i, rule := range rules {
		numCaptures++
		groups[i] = numCaptures
		re, _ := rule.re.source()
		keep := !ruleGroupsLeftOut || rule.opts.groups
		part, refers := joinedRule(re, numCaptures, keep)
		if refers && !keep {
			keep = true
			part, _ = joinedRule(re, numCaptures, keep)
		}
		if keep {
			// Named groups take a number too, which BACKREF_RE does not count.
			numCaptures += countMatchGroups(rule.re)
		}
		parts[i] = part
	}
	return strings.Join(parts, "|"), groups
}

// joinedRule is one pattern of a joined expression in its group, the group that has the number offset: its backreferences are renumbered, and its own groups are written as they are (keep) or as non-capturing. refers reports that the pattern refers to a group, read as BACKREF_RE reads it: a backreference or a named group.
func joinedRule(re string, offset int, keep bool) (part string, refers bool) {
	var out strings.Builder
	out.WriteByte('(')
	for re != "" {
		loc := backrefRE.FindStringSubmatchIndex(re)
		if loc == nil {
			out.WriteString(re)
			break
		}
		out.WriteString(re[:loc[0]])
		match := re[loc[0]:loc[1]]
		re = re[loc[1]:]
		switch {
		case match[0] == '\\' && loc[2] >= 0:
			refers = true
			n, _ := strconv.Atoi(match[1:])
			out.WriteString(`\` + strconv.Itoa(n+offset))
		case match == "(" && !keep:
			out.WriteString("(?:")
		default:
			if match == `\k` || match == "(?" && strings.HasPrefix(re, "<") && !strings.HasPrefix(re, "<=") && !strings.HasPrefix(re, "<!") {
				refers = true
			}
			out.WriteString(match)
		}
	}
	out.WriteByte(')')
	return out.String(), refers
}

// either is highlight.js either(...args).
func either(args []pattern) string {
	sources := make([]string, len(args))
	for i, arg := range args {
		sources[i], _ = arg.source()
	}
	return "(" + strings.Join(sources, "|") + ")"
}

// escapeRegExp is highlight.js escape(value): a RegExp matching value literally, with the m flag only.
func escapeRegExp(value string) *jsRegExp {
	var out strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`-/\^$*+?.()|[]{}`, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	re, err := newJSRegExp(out.String(), false)
	if err != nil {
		throw(err.Error())
	}
	return re
}

// langRe is compileLanguage's langRe: new RegExp(source(value), "m" + (case_insensitive ? "i" : "")). source(value) of a falsy value is null, which RegExp reads as "null".
func langRe(language *Mode, value pattern) *jsRegExp {
	re := compileLangRe(language, value)
	props := language.setLang()
	props.sources = append(props.sources, re.source)
	return re
}

// compileLangRe is langRe without recording the pattern among the language's sources.
func compileLangRe(language *Mode, value pattern) *jsRegExp {
	source, ok := value.source()
	if !ok {
		source = "null"
	}
	re, err := newJSRegExp(source, language.caseInsensitive)
	if err != nil {
		throw(err.Error())
	}
	return re
}

var commonKeywords = []string{"of", "and", "for", "in", "not", "or", "if", "then", "parent", "list", "value"}

// compileKeywords is highlight.js compileKeywords for a string, an array, or (via compileKeywordsObject) an object of class names.
func compileKeywords(compiled map[string]keywordData, keywords []string, caseInsensitive bool, className string) {
	for _, keyword := range keywords {
		if caseInsensitive {
			keyword = toLowerCase(keyword)
		}
		word, score, hasScore := strings.Cut(keyword, "|")
		if hasScore {
			score, _, _ = strings.Cut(score, "|")
		}
		compiled[word] = keywordData{kind: className, relevance: scoreForKeyword(word, score)}
	}
}

func compileKeywordsValue(raw keywordsValue, caseInsensitive bool) map[string]keywordData {
	compiled := map[string]keywordData{}
	switch raw.kind {
	case keywordsKindString:
		compileKeywords(compiled, strings.Split(raw.text, " "), caseInsensitive, "keyword")
	case keywordsKindList:
		compileKeywords(compiled, raw.list, caseInsensitive, "keyword")
	case keywordsKindObject:
		for _, class := range raw.object.classes {
			if class.isList {
				compileKeywords(compiled, class.list, caseInsensitive, class.name)
			} else {
				compileKeywords(compiled, strings.Split(class.text, " "), caseInsensitive, class.name)
			}
		}
	}
	return compiled
}

// scoreForKeyword: a provided score wins (Number(score)); otherwise common keywords score 0 and others 1.
func scoreForKeyword(keyword, providedScore string) float64 {
	if providedScore != "" {
		return jsnumber.Parse(providedScore)
	}
	if slices.Contains(commonKeywords, toLowerCase(keyword)) {
		return 0
	}
	return 1
}

// skipIfhasPrecedingDot is the internal beginKeywords callback.
func skipIfhasPrecedingDot(match *enhancedMatch, response *response) {
	if match.index-1 >= 0 && match.input[match.index-1] == '.' {
		response.ignoreMatch()
	}
}

func extBeginKeywords(mode, parent *Mode) {
	if parent == nil || mode.rare().beginKeywords == "" {
		return
	}
	mode.begin = stringPattern(`\b(` + strings.Join(strings.Split(mode.rare().beginKeywords, " "), "|") + `)(?!\.)(?=\b|\s)`)
	mode.present |= keyBegin
	mode.setComp().beforeBegin = skipIfhasPrecedingDot
	mode.present |= keyBeforeBegin
	if !mode.rare().keywords.truthy() {
		mode.setRare().keywords = keywordsValue{kind: keywordsKindString, text: mode.rare().beginKeywords}
	}
	mode.present |= keyKeywords
	deleteKey(mode, keyBeginKeywords)
	if !mode.hasRelevance {
		mode.relevance, mode.hasRelevance = 0, true
		mode.present |= keyRelevance
	}
}

func extCompileIllegal(mode, _ *Mode) {
	if mode.rare().illegal.kind != patternList {
		return
	}
	mode.setRare().illegal = stringPattern(either(mode.rare().illegal.items()))
}

func extCompileMatch(mode, _ *Mode) {
	if !mode.rare().match.truthy() {
		return
	}
	if mode.begin.truthy() || mode.end.truthy() {
		throw("begin & end are not supported with match")
	}
	mode.begin = mode.rare().match
	mode.present |= keyBegin
	deleteKey(mode, keyMatch)
}

func extCompileRelevance(mode, _ *Mode) {
	if !mode.hasRelevance {
		mode.relevance, mode.hasRelevance = 1, true
		mode.present |= keyRelevance
	}
}

// compileLanguage is highlight.js compileLanguage. It runs on every highlight; compileMode returns a compiled mode unchanged.
func compileLanguage(language *Mode) *Mode {
	props := language.setLang()
	if language.present&keyCompilerExtensions == 0 || props.compilerExtensions == nil {
		props.compilerExtensions = []compilerExtension{}
		language.present |= keyCompilerExtensions
	}
	for _, c := range language.contains {
		if c.self {
			throw("ERR: contains `self` is not supported at the top-level of a language.  See documentation.")
		}
	}
	aliases := make(map[string]string, len(props.classNameAliases))
	maps.Copy(aliases, props.classNameAliases)
	props.classNameAliases = aliases
	language.present |= keyClassNameAliases
	return compileMode(language, language, nil)
}

func compileMode(language, mode, parent *Mode) *Mode {
	if mode.isCompiled {
		return mode
	}
	extCompileMatch(mode, parent)
	for _, ext := range language.lang().compilerExtensions {
		ext(mode, parent)
	}
	if mode.compiled != nil {
		mode.compiled.beforeBegin = nil
	}
	mode.present |= keyBeforeBegin
	extBeginKeywords(mode, parent)
	extCompileIllegal(mode, parent)
	extCompileRelevance(mode, parent)
	mode.isCompiled = true
	mode.present |= keyIsCompiled

	var keywordPattern pattern
	if mode.rare().keywords.kind == keywordsKindObject {
		object := mode.rare().keywords.object
		if object.hasPattern {
			keywordPattern = object.pattern
		}
		object.pattern, object.hasPattern = pattern{}, false
	}
	if mode.rare().keywords.truthy() {
		mode.setRare().keywords = keywordsValue{kind: keywordsKindCompiled, compiled: compileKeywordsValue(mode.rare().keywords, language.caseInsensitive)}
	}
	if mode.rare().lexemes.truthy() && keywordPattern.truthy() {
		throw("ERR: Prefer `keywords.$pattern` to `mode.lexemes`, BOTH are not allowed. (see mode reference) ")
	}
	switch {
	case keywordPattern.truthy():
	case mode.rare().lexemes.truthy():
		keywordPattern = mode.rare().lexemes
	default:
		keywordPattern = regExpPattern(`\w+`)
	}
	mode.setComp().keywordPatternRe = langRe(language, keywordPattern)
	mode.setComp().lexemes = newLexemeScanner(mode.comp().keywordPatternRe.source, language.caseInsensitive)
	mode.present |= keyKeywordPatternRe

	if parent != nil {
		if !mode.begin.truthy() {
			mode.begin = regExpPattern(`\B|\b`)
			mode.present |= keyBegin
		}
		mode.setComp().beginRe = langRe(language, mode.begin)
		mode.present |= keyBeginRe
		if mode.endSameAsBegin {
			mode.end = mode.begin
			mode.present |= keyEnd
		}
		if !mode.end.truthy() && !mode.endsWithParent {
			mode.end = regExpPattern(`\B|\b`)
			mode.present |= keyEnd
		}
		if mode.end.truthy() {
			mode.setComp().endRe = langRe(language, mode.end)
			mode.present |= keyEndRe
		}
		compiled := mode.setComp()
		compiled.terminatorEnd, _ = mode.end.source()
		mode.present |= keyTerminatorEnd
		if parentEnd := parent.comp().terminatorEnd; mode.endsWithParent && parentEnd != "" {
			if mode.end.truthy() {
				compiled.terminatorEnd += "|"
			}
			compiled.terminatorEnd += parentEnd
		}
	}
	if mode.rare().illegal.truthy() {
		mode.setComp().illegalRe = langRe(language, mode.rare().illegal)
		mode.present |= keyIllegalRe
	}
	contains := []*Mode{}
	for _, c := range mode.contains {
		child := c.mode
		if c.self {
			child = mode
		}
		contains = append(contains, expandOrCloneMode(child)...)
	}
	mode.contains = make([]modeRef, len(contains))
	for i, c := range contains {
		mode.contains[i] = modeRef{mode: c}
	}
	mode.present |= keyContains
	for _, c := range contains {
		compileMode(language, c, mode)
	}
	if mode.starts != nil {
		compileMode(language, mode.starts, parent)
	}
	mode.setComp().matcher = buildModeRegex(language, mode)
	mode.present |= keyMatcher
	return mode
}

func buildModeRegex(language, mode *Mode) *resumableMultiRegex {
	mm := &resumableMultiRegex{multiRegexes: map[int]*multiRegex{}, language: language}
	for _, term := range mode.contains {
		mm.addRule(term.mode.begin, &matchOptions{rule: term.mode, typ: "begin", groups: term.mode.rare().onBegin != nil || term.mode.comp().beforeBegin != nil})
	}
	if terminatorEnd := mode.comp().terminatorEnd; terminatorEnd != "" {
		// The end is read by on:end of the mode, and of the modes it ends with.
		mm.addRule(stringPattern(terminatorEnd), &matchOptions{typ: "end", groups: mode.rare().onEnd != nil || mode.endsWithParent})
	}
	if mode.rare().illegal.truthy() {
		mm.addRule(mode.rare().illegal, &matchOptions{typ: "illegal"})
	}
	return mm
}

func dependencyOnParent(mode *Mode) bool {
	if mode == nil {
		return false
	}
	return mode.endsWithParent || dependencyOnParent(mode.starts)
}

// expandOrCloneMode replaces a mode with its variants, or clones it when it depends on its parent or is frozen.
func expandOrCloneMode(mode *Mode) []*Mode {
	if mode.hasVariants && !mode.hasCachedVariants {
		variants := make([]*Mode, len(mode.variants))
		for i, variant := range mode.variants {
			variants[i] = inherit(mode, &Mode{present: keyVariants}, variant)
		}
		mode.setComp().cachedVariants, mode.hasCachedVariants = variants, true
		mode.present |= keyCachedVariants
	}
	if mode.hasCachedVariants {
		return mode.comp().cachedVariants
	}
	if dependencyOnParent(mode) {
		override := &Mode{present: keyStarts}
		if mode.starts != nil {
			override.starts = inherit(mode.starts)
		}
		return []*Mode{inherit(mode, override)}
	}
	if mode.frozen {
		return []*Mode{inherit(mode)}
	}
	return []*Mode{mode}
}

// Result is a highlight result. Value is the HTML highlight.js renders; its text is WTF-8, keeping JavaScript's lone surrogate code units.
type Result struct {
	Relevance   float64
	Value       string
	Language    string
	HasLanguage bool
	Illegal     bool
	emitter     *tokenTreeEmitter
	top         *frame
	// failed is set when run recovered a highlight.js exception and returned the escaped code instead of a token tree.
	failed bool
	// checkpoints are the resumable states the run passed, when it recorded them.
	checkpoints []checkpoint
	// iterations is the scan iterations the run counted, those before its starting checkpoint included.
	iterations int
}

// checkpoint is a state at the start of a scan iteration from which a run over the same code, with more text after it, continues identically: nothing is buffered, and it holds what the run keeps besides. unit is where the scan stands, and iterations the count of the iterations before it, which a resumed run starts from, so highlight.js's iteration limit throws at the same match as in a whole run. top is the mode stack, nil for the language alone. appends and depth place it in the emitter's output: the children added before it and the nodes open at it. The counts are held in 32 bits, since a run records a checkpoint for every few units of its code; a run past them records no more.
type checkpoint struct {
	unit       int32
	iterations int32
	appends    int32
	depth      int32
	top        *frame
}

// recording is the checkpoints a run records.
type recording uint8

const (
	recordNone recording = iota
	// recordRoot records the checkpoints between finished top-level nodes.
	recordRoot
	// recordStack records the checkpoints inside open modes as well.
	recordStack
)

type highlightRun struct {
	registry       *Registry
	languageName   string
	code           []rune
	ignoreIllegals bool
	language       *Mode
	top            *frame
	continuations  map[string]*frame
	emitter        *tokenTreeEmitter
	modeBuffer     []rune
	relevance      float64
	index          int
	iterations     int
	resume         bool
	// lastMatchType and lastMatchIndex are what the highlighter reads of lastMatch: the type ("" before the first match) and the index of the match processed last. match is the match of the iteration that is running: nothing keeps one past its iteration.
	lastMatchType  string
	lastMatchIndex int
	match          enhancedMatch
	record         recording
	checkpoints    []checkpoint
	// keyBuffer is the bytes of the lexeme keywordData looks up.
	keyBuffer []byte
	// beginResponse is the Response of the begin match being processed.
	beginResponse response
}

// highlightJSIterationLimit is the iterations after which highlight.js throws, when they also exceed three times the match index.
// upstream: packages/coding-agent/src/core/export-html/vendor/highlight.min.js:iterations
const highlightJSIterationLimit = 100000

// highlight is highlight.js _highlight(languageName, codeToHighlight, ignoreIllegals, continuation).
func (r *Registry) highlight(languageName string, code []rune, ignoreIllegals bool, continuation *frame) Result {
	return r.highlightFrom(languageName, code, ignoreIllegals, continuation, checkpoint{}, recordNone)
}

// highlightFrom is highlight starting at from, a checkpoint of an earlier run over a prefix of code (the zero checkpoint for the whole code): at its unit, with its iteration count, on its mode stack. record is the checkpoints this run records. A stack that does not stand on the root frame this Registry has for the language is not continued: the run fails.
func (r *Registry) highlightFrom(languageName string, code []rune, ignoreIllegals bool, continuation *frame, from checkpoint, record recording) Result {
	language := r.getLanguage(languageName)
	if language == nil {
		throw(`Unknown language: "` + languageName + `"`)
	}
	md := compileLanguage(language)
	run := &highlightRun{
		registry:       r,
		languageName:   languageName,
		code:           code,
		ignoreIllegals: ignoreIllegals,
		language:       md,
		continuations:  map[string]*frame{},
		emitter:        newTokenTreeEmitter(),
		index:          int(from.unit),
		iterations:     int(from.iterations),
		record:         record,
	}
	run.top = continuation
	if run.top == nil {
		run.top = languageFrame(md)
	}
	if from.top == nil {
		run.processContinuations()
		return run.run()
	}
	// The root frame of a language is unique in its Registry, so a stack of another language, of another Registry, or of a language registered again does not stand on it.
	bottom := from.top
	for bottom.parent != nil {
		bottom = bottom.parent
	}
	if continuation != nil || bottom != run.top || int(from.unit) > len(code) {
		return Result{Language: languageName, HasLanguage: true, emitter: run.emitter, top: run.top, failed: true}
	}
	run.top = from.top
	run.reopenNodes()
	return run.run()
}

func languageFrame(language *Mode) *frame {
	props := language.setLang()
	if props.rootFrame == nil {
		props.rootFrame = &frame{mode: language, language: true}
	}
	return props.rootFrame
}

func (h *highlightRun) run() (result Result) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err, ok := recovered.(*hljsError)
		if !ok {
			panic(recovered)
		}
		if strings.Contains(err.message, "Illegal") {
			result = Result{Illegal: true, Value: escapeHTML(unitsToString(h.code)), emitter: h.emitter, failed: true}
			return
		}
		result = Result{Value: escapeHTML(unitsToString(h.code)), Language: h.languageName, HasLanguage: true, emitter: h.emitter, top: h.top, failed: true}
	}()
	h.top.mode.comp().matcher.considerAll()
	for {
		h.iterations++
		if h.record != recordNone {
			h.recordCheckpoint()
		}
		if h.resume {
			h.resume = false
		} else {
			h.top.mode.comp().matcher.considerAll()
		}
		h.top.mode.comp().matcher.lastIndex = h.index
		match := h.top.mode.comp().matcher.exec(h.code, &h.match)
		if match == nil {
			break
		}
		beforeMatch := substring(h.code, h.index, match.index)
		processed := h.processLexeme(beforeMatch, match)
		h.index = match.index + processed
	}
	h.processLexeme(substr(h.code, h.index), nil)
	h.emitter.closeAllNodes()
	return Result{
		Relevance:   math.Floor(h.relevance),
		Value:       h.emitter.toHTML(),
		Language:    h.languageName,
		HasLanguage: true,
		emitter:     h.emitter,
		top:         h.top,
		checkpoints: h.checkpoints,
		iterations:  h.iterations,
	}
}

// languageReach is the greatest reach of any pattern of the compiled language.
func languageReach(language *Mode) reach {
	props := language.setLang()
	if !props.reachDone {
		props.reach = reach{}
		for _, source := range props.sources {
			r := patternReach(source)
			if guard, ok := guardedSources[source]; ok && r.unbounded {
				// The check bounds what the pattern reads past a point; what stays is the line break of its own, as the
				// patterns that match one count.
				r.unbounded, r.lines = false, 1
				props.reach.guards |= guard
			}
			if source == xmlTagBegin {
				props.reach.guards |= guardAngle
			}
			props.reach.lines = max(props.reach.lines, r.lines)
			props.reach.unbounded = props.reach.unbounded || r.unbounded
			props.reach.spaceRun = props.reach.spaceRun || r.spaceRun
		}
		props.reachDone = true
	}
	return props.reach
}

// recordCheckpoint records the state at the start of this iteration when a run over more text would reach it unchanged and a run that starts from it can be given all of it. A state is not recorded for what a run keeps outside a checkpoint: text the mode on top has not emitted, a match being retried (resume), the stack of a sublanguage that did not end on its root (continuations), a begin match at the position of the next match (lastMatch), and what a mode on the stack was given when it began (stackResumes).
func (h *highlightRun) recordCheckpoint() {
	if h.resume || len(h.modeBuffer) != 0 || h.index == 0 || h.index >= len(h.code) {
		return
	}
	if !h.top.language && (h.record != recordStack || !stackResumes(h.top)) {
		return
	}
	for _, continuation := range h.continuations {
		// A sublanguage run that ended on its root continues as a new run does.
		if continuation != nil && !continuation.language {
			return
		}
	}
	// Only a begin match at the position of the next match changes how that match is read, and a resumed run has no last match.
	if h.lastMatchType == "begin" && h.lastMatchIndex >= h.index {
		return
	}
	if isLowSurrogate(h.code[h.index]) || isHighSurrogate(h.code[h.index-1]) {
		return
	}
	if h.index > math.MaxInt32 || h.iterations > math.MaxInt32 || h.emitter.appends > math.MaxInt32 {
		return
	}
	n := len(h.checkpoints)
	if n > 0 && int(h.checkpoints[n-1].unit) == h.index {
		return
	}
	point := checkpoint{unit: int32(h.index), iterations: int32(h.iterations - 1), appends: int32(h.emitter.appends), depth: int32(len(h.emitter.stack) - 1)}
	if !h.top.language {
		point.top = h.top
		// A point is valid by the lines that follow it (resumeLimit), so the last point of a line inside open modes stands for the line.
		if n > 0 && h.checkpoints[n-1].top != nil && !slices.Contains(h.code[h.checkpoints[n-1].unit:h.index], '\n') {
			h.checkpoints[n-1] = point
			return
		}
	}
	h.checkpoints = append(h.checkpoints, point)
}

// ResumeStackMax bounds the modes of a stack a checkpoint holds, and so the frames a caller keeps with a ResumePoint, and the spans open at one.
const ResumeStackMax = 64

// stackResumes reports whether a run can start on the stack: it has at most ResumeStackMax modes, and none of them holds what a run gave it when the mode began, or buffers text for another language. The callbacks of a mode (on:begin, on:end) keep what they share on the mode object (END_SAME_AS_BEGIN's first group), endSameAsBegin and the mode it starts keep the end pattern of their begin there, and a sublanguage mode buffers its text for a run of the other language.
func stackResumes(top *frame) bool {
	depth := 0
	for f := top; !f.language; f = f.parent {
		depth++
		mode := f.mode
		if depth > ResumeStackMax || mode.endSameAsBegin || mode.comp().endFromBegin || f.data != nil || mode.rare().onBegin != nil || mode.rare().onEnd != nil || mode.rare().subLanguage.set {
			return false
		}
	}
	return true
}

// nodeClass is the class of the node a mode opens in the output, "" for a mode that opens none.
func (h *highlightRun) nodeClass(mode *Mode) string {
	if mode.className == "" {
		return ""
	}
	if className := h.language.lang().classNameAliases[mode.className]; className != "" {
		return className
	}
	return mode.className
}

// reopenNodes opens the nodes of the modes on the stack a run continues from a checkpoint, outermost first, as startNewMode opened them in the run that recorded it: with the language's aliases, which highlight.js processContinuations, for the stack of a sublanguage, does not apply.
func (h *highlightRun) reopenNodes() {
	var classes []string
	for f := h.top; !f.language; f = f.parent {
		if className := h.nodeClass(f.mode); className != "" {
			classes = append(classes, className)
		}
	}
	for _, className := range slices.Backward(classes) {
		h.emitter.openNode(className)
	}
	h.emitter.continued = len(classes)
}

func isHighSurrogate(u rune) bool { return u >= 0xd800 && u <= 0xdbff }
func isLowSurrogate(u rune) bool  { return u >= 0xdc00 && u <= 0xdfff }

func (h *highlightRun) keywordData(mode *Mode, lexeme []rune) (keywordData, bool) {
	compiled := mode.rare().keywords.compiled
	// An ASCII lexeme is looked up from the run's buffer, with no string of its own.
	key := h.keyBuffer[:0]
	for _, u := range lexeme {
		if u >= utf8.RuneSelf {
			matchText := unitsToString(lexeme)
			if h.language.caseInsensitive {
				matchText = toLowerCase(matchText)
			}
			data, ok := compiled[matchText]
			return data, ok
		}
		if h.language.caseInsensitive && u >= 'A' && u <= 'Z' {
			u += 'a' - 'A'
		}
		key = append(key, byte(u))
	}
	h.keyBuffer = key
	data, ok := compiled[string(key)]
	return data, ok
}

func (h *highlightRun) processKeywords() {
	top := h.top.mode
	if !top.rare().keywords.truthy() {
		h.emitter.addText(unitsToString(h.modeBuffer))
		return
	}
	// The text from pending is not emitted yet: it runs through the lexemes that are no keyword, and those a keyword kind that starts with _ leaves as text.
	pending, lastIndex := 0, 0
	for {
		start, end, ok := top.comp().nextLexeme(h.modeBuffer, lastIndex)
		if !ok {
			break
		}
		text := h.modeBuffer[start:end]
		if data, ok := h.keywordData(top, text); ok {
			h.emitter.addText(unitsToString(h.modeBuffer[pending:start]))
			h.relevance += data.relevance
			if strings.HasPrefix(data.kind, "_") {
				pending = start
			} else {
				cssClass := h.language.lang().classNameAliases[data.kind]
				if cssClass == "" {
					cssClass = data.kind
				}
				h.emitter.addKeyword(unitsToString(text), cssClass)
				pending = end
			}
		}
		lastIndex = end
	}
	h.emitter.addText(unitsToString(h.modeBuffer[pending:]))
}

func (h *highlightRun) processSubLanguage() {
	if len(h.modeBuffer) == 0 {
		return
	}
	top := h.top.mode
	var result Result
	if !top.rare().subLanguage.isList {
		if h.registry.registeredLanguage(top.rare().subLanguage.name) == nil {
			h.emitter.addText(unitsToString(h.modeBuffer))
			return
		}
		result = h.registry.highlight(top.rare().subLanguage.name, h.modeBuffer, true, h.continuations[top.rare().subLanguage.name])
		h.continuations[top.rare().subLanguage.name] = result.top
	} else {
		var subset []string
		if len(top.rare().subLanguage.list) > 0 {
			subset = top.rare().subLanguage.list
		}
		result = h.registry.highlightAuto(h.modeBuffer, subset)
	}
	if top.relevance > 0 {
		h.relevance += result.Relevance
	}
	h.emitter.addSublanguage(result.emitter, result.Language)
}

func (h *highlightRun) processBuffer() {
	if h.top.mode.rare().subLanguage.set {
		h.processSubLanguage()
	} else {
		h.processKeywords()
	}
	// The buffer's array is used again: what was in it is emitted, and a sublanguage run that read it has ended.
	h.modeBuffer = h.modeBuffer[:0]
}

func (h *highlightRun) startNewMode(mode *Mode) *frame {
	if className := h.nodeClass(mode); className != "" {
		h.emitter.openNode(className)
	}
	h.top = &frame{mode: mode, parent: h.top}
	return h.top
}

func (h *highlightRun) endOfMode(mode *frame, match *enhancedMatch, matchPlusRemainder []rune) *frame {
	endRe := mode.mode.comp().endRe
	matched := endRe != nil && endRe.matchesAtStart(matchPlusRemainder)
	if matched {
		if mode.mode.rare().onEnd != nil {
			resp := mode.response()
			mode.mode.rare().onEnd(match, resp)
			if resp.isMatchIgnored {
				matched = false
			}
		}
		if matched {
			for mode.mode.endsParent && mode.parent != nil {
				mode = mode.parent
			}
			return mode
		}
	}
	if mode.mode.endsWithParent {
		return h.endOfMode(mode.parent, match, matchPlusRemainder)
	}
	return nil
}

// doIgnore keeps scanning at the same position when other rules remain, or moves past one code unit.
func (h *highlightRun) doIgnore(lexeme []rune) int {
	if h.top.mode.comp().matcher.regexIndex == 0 {
		if len(lexeme) == 0 {
			// lexeme[0] of an empty lexeme is undefined, which string concatenation spells out.
			h.modeBuffer = append(h.modeBuffer, []rune("undefined")...)
		} else {
			h.modeBuffer = append(h.modeBuffer, lexeme[0])
		}
		return 1
	}
	h.resume = true
	return 0
}

func (h *highlightRun) doBeginMatch(match *enhancedMatch) int {
	lexeme := match.lexeme()
	newMode := match.rule()
	// The callbacks of a begin run one after the other and keep no response: the run has one for them.
	h.beginResponse = response{data: ruleData(newMode)}
	resp := &h.beginResponse
	for _, cb := range []callback{newMode.comp().beforeBegin, newMode.rare().onBegin} {
		if cb == nil {
			continue
		}
		cb(match, resp)
		if resp.isMatchIgnored {
			return h.doIgnore(lexeme)
		}
	}
	if newMode.endSameAsBegin {
		newMode.setComp().endRe = escapeRegExp(unitsToString(lexeme))
		newMode.present |= keyEndRe
	}
	if newMode.skip {
		h.modeBuffer = append(h.modeBuffer, lexeme...)
	} else {
		if newMode.excludeBegin {
			h.modeBuffer = append(h.modeBuffer, lexeme...)
		}
		h.processBuffer()
		if !newMode.returnBegin && !newMode.excludeBegin {
			h.modeBuffer = append(h.modeBuffer[:0], lexeme...)
		}
	}
	h.startNewMode(newMode)
	if newMode.returnBegin {
		return 0
	}
	return len(lexeme)
}

const noMatch = -1

func (h *highlightRun) doEndMatch(match *enhancedMatch) int {
	lexeme := match.lexeme()
	matchPlusRemainder := substr(h.code, match.index)
	endMode := h.endOfMode(h.top, match, matchPlusRemainder)
	if endMode == nil {
		return noMatch
	}
	origin := h.top
	if origin.mode.skip {
		h.modeBuffer = append(h.modeBuffer, lexeme...)
	} else {
		if !origin.mode.returnEnd && !origin.mode.excludeEnd {
			h.modeBuffer = append(h.modeBuffer, lexeme...)
		}
		h.processBuffer()
		if origin.mode.excludeEnd {
			h.modeBuffer = append(h.modeBuffer[:0], lexeme...)
		}
	}
	for {
		if h.top.mode.className != "" {
			h.emitter.closeNode()
		}
		if !h.top.mode.skip && !h.top.mode.rare().subLanguage.truthy() {
			h.relevance += h.top.mode.relevance
		}
		h.top = h.top.parent
		if h.top == endMode.parent {
			break
		}
	}
	if endMode.mode.starts != nil {
		if endMode.mode.endSameAsBegin {
			endMode.mode.starts.setComp().endRe = endMode.mode.comp().endRe
			endMode.mode.starts.setComp().endFromBegin = true
			endMode.mode.starts.present |= keyEndRe
		}
		h.startNewMode(endMode.mode.starts)
	}
	if origin.mode.returnEnd {
		return 0
	}
	return len(lexeme)
}

func (h *highlightRun) processContinuations() {
	var list []string
	for current := h.top; !current.language; current = current.parent {
		if current.mode.className != "" {
			list = append([]string{current.mode.className}, list...)
		}
	}
	for _, item := range list {
		h.emitter.openNode(item)
	}
}

func (h *highlightRun) processLexeme(textBeforeMatch []rune, match *enhancedMatch) int {
	h.modeBuffer = append(h.modeBuffer, textBeforeMatch...)
	if match == nil {
		h.processBuffer()
		return 0
	}
	lexeme := match.lexeme()
	if h.lastMatchType == "begin" && match.matchType() == "end" && h.lastMatchIndex == match.index && len(lexeme) == 0 {
		h.modeBuffer = append(h.modeBuffer, slice(h.code, match.index, match.index+1)...)
		return 1
	}
	h.lastMatchType, h.lastMatchIndex = match.matchType(), match.index
	switch {
	case match.matchType() == "begin":
		return h.doBeginMatch(match)
	case match.matchType() == "illegal" && !h.ignoreIllegals:
		className := h.top.mode.className
		if className == "" {
			className = "<unnamed>"
		}
		throw(`Illegal lexeme "` + unitsToString(lexeme) + `" for mode "` + className + `"`)
	case match.matchType() == "end":
		if processed := h.doEndMatch(match); processed != noMatch {
			return processed
		}
	}
	if match.matchType() == "illegal" && len(lexeme) == 0 {
		return 1
	}
	if h.iterations > highlightJSIterationLimit && h.iterations > match.index*3 {
		throw("potential infinite loop, way more iterations than matches")
	}
	h.modeBuffer = append(h.modeBuffer, lexeme...)
	return len(lexeme)
}

// highlightAuto is highlight.js highlightAuto(code, languageSubset).
func (r *Registry) highlightAuto(code []rune, languageSubset []string) Result {
	if languageSubset == nil {
		// Auto-detection runs every language, so the rest of the full set is decoded now.
		r.decodeAllDeferred()
		languageSubset = slices.Clone(r.order)
	}
	plaintext := Result{Value: escapeHTML(unitsToString(code)), emitter: newTokenTreeEmitter()}
	plaintext.emitter.addText(unitsToString(code))
	results := []Result{plaintext}
	for _, name := range languageSubset {
		language := r.getLanguage(name)
		if language == nil || language.disableAutodetect {
			continue
		}
		results = append(results, r.highlight(name, code, false, nil))
	}
	jsarray.Sort(results, func(a, b Result) float64 {
		if a.Relevance != b.Relevance {
			return b.Relevance - a.Relevance
		}
		if a.HasLanguage && b.HasLanguage {
			if r.getLanguage(a.Language).lang().supersetOf == b.Language {
				return 1
			}
			if r.getLanguage(b.Language).lang().supersetOf == a.Language {
				return -1
			}
		}
		return 0
	})
	return results[0]
}

func substring(units []rune, start, end int) []rune {
	start = min(max(start, 0), len(units))
	end = min(max(end, 0), len(units))
	if start > end {
		start, end = end, start
	}
	return units[start:end]
}

func substr(units []rune, start int) []rune {
	if start >= len(units) {
		return nil
	}
	return units[max(start, 0):]
}

func slice(units []rune, start, end int) []rune {
	start = min(max(start, 0), len(units))
	end = min(max(end, 0), len(units))
	if start >= end {
		return nil
	}
	return units[start:end]
}

// unitsToString encodes UTF-16 code units as WTF-8: pairs become their code point, lone surrogates keep their unit.
func unitsToString(units []rune) string {
	ascii := true
	for _, u := range units {
		if u >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		b := make([]byte, len(units))
		for i, u := range units {
			b[i] = byte(u)
		}
		return string(b)
	}
	if len(units) > math.MaxInt32 {
		units = units[:math.MaxInt32]
	}
	capHint := len(units)
	if capHint < math.MaxInt32-8 {
		capHint += 8
	}
	out := make([]byte, 0, capHint)
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xd800 && u <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			out = utf8.AppendRune(out, utf16.DecodeRune(u, units[i+1]))
			i++
			continue
		}
		if u >= 0xd800 && u <= 0xdfff {
			out = append(out, byte(0xe0|(u>>12)), byte(0x80|((u>>6)&0x3f)), byte(0x80|(u&0x3f)))
			continue
		}
		out = utf8.AppendRune(out, u)
	}
	return string(out)
}

// stringToUnits decodes WTF-8 (or UTF-8) into UTF-16 code units.
func stringToUnits(text string) []rune { return appendUnits(make([]rune, 0, len(text)), text) }

// appendUnits appends the UTF-16 code units of the WTF-8 (or UTF-8) text to units.
func appendUnits(units []rune, text string) []rune {
	for len(text) > 0 {
		if text[0] < utf8.RuneSelf {
			units = append(units, rune(text[0]))
			text = text[1:]
			continue
		}
		if len(text) >= 3 && text[0] == 0xed && text[1] >= 0xa0 && text[1] <= 0xbf && text[2] >= 0x80 && text[2] <= 0xbf {
			units = append(units, rune(text[0]&15)<<12|rune(text[1]&63)<<6|rune(text[2]&63))
			text = text[3:]
			continue
		}
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]
		if r > 0xffff {
			a, b := utf16.EncodeRune(r)
			units = append(units, a, b)
			continue
		}
		units = append(units, r)
	}
	return units
}
