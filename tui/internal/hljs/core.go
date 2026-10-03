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
	if mode.data == nil {
		mode.data = &responseData{}
		mode.present |= keyData
	}
	return &response{data: mode.data}
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
		if f.mode.data != nil {
			return &response{data: f.mode.data}
		}
		f.data = &responseData{}
	}
	return &response{data: f.data}
}

// enhancedMatch is a MultiRegex exec result after match.splice(0, i) and Object.assign(match, matchData).
type enhancedMatch struct {
	input    []rune
	index    int
	spans    []int
	offset   int
	opts     *matchOptions
	position int
}

// group is match[k]: the code units of a participating group, ok false for undefined.
func (m *enhancedMatch) group(k int) ([]rune, bool) {
	g := m.offset + k
	if 2*g+1 >= len(m.spans) || m.spans[2*g] < 0 {
		return nil, false
	}
	return m.input[m.spans[2*g]:m.spans[2*g+1]], true
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

// matchOptions is the opts object a rule is registered with. It is shared by every MultiRegex built from the same rules, and each addRule renumbers its position.
type matchOptions struct {
	rule     *Mode
	typ      string
	position int
}

type multiRegexRule struct {
	re   pattern
	opts *matchOptions
}

type multiRegex struct {
	matchIndexes map[int]*matchOptions
	regexes      []multiRegexRule
	matchAt      int
	position     int
	matcherRe    *jsRegExp
	empty        bool
	lastIndex    int
}

func newMultiRegex() *multiRegex {
	return &multiRegex{matchIndexes: map[int]*matchOptions{}, matchAt: 1}
}

func (m *multiRegex) addRule(re pattern, opts *matchOptions) {
	opts.position = m.position
	m.position++
	m.matchIndexes[m.matchAt] = opts
	m.regexes = append(m.regexes, multiRegexRule{re, opts})
	m.matchAt += countMatchGroups(re) + 1
}

func (m *multiRegex) compile(language *Mode) {
	if len(m.regexes) == 0 {
		m.empty = true
	}
	terminators := make([]pattern, len(m.regexes))
	for i, r := range m.regexes {
		terminators[i] = r.re
	}
	m.matcherRe = langRe(language, stringPattern(join(terminators)))
}

func (m *multiRegex) exec(s []rune) *enhancedMatch {
	if m.empty {
		return nil
	}
	match := m.matcherRe.exec(s, m.lastIndex)
	if match == nil {
		return nil
	}
	i := -1
	for g := 1; 2*g+1 < len(match.spans); g++ {
		if match.spans[2*g] >= 0 {
			i = g
			break
		}
	}
	result := &enhancedMatch{input: s, index: match.index, spans: match.spans, offset: max(i, 0)}
	if i >= 0 {
		if opts := m.matchIndexes[i]; opts != nil {
			result.opts = opts
			result.position = opts.position
		}
	}
	return result
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
	r.rules = append(r.rules, multiRegexRule{re, opts})
	if opts.typ == "begin" {
		r.count++
	}
}

func (r *resumableMultiRegex) exec(s []rune) *enhancedMatch {
	m := r.getMatcher(r.regexIndex)
	m.lastIndex = r.lastIndex
	result := m.exec(s)
	if r.resumingScanAtSamePosition() {
		if result == nil || result.index != r.lastIndex {
			m2 := r.getMatcher(0)
			m2.lastIndex = r.lastIndex + 1
			result = m2.exec(s)
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

// join is highlight.js join(regexps, "|"): each pattern in its own group, with backreferences renumbered.
func join(regexps []pattern) string {
	numCaptures := 0
	parts := make([]string, len(regexps))
	for i, regex := range regexps {
		numCaptures++
		offset := numCaptures
		re, _ := regex.source()
		var out strings.Builder
		for re != "" {
			loc := backrefRE.FindStringSubmatchIndex(re)
			if loc == nil {
				out.WriteString(re)
				break
			}
			out.WriteString(re[:loc[0]])
			match := re[loc[0]:loc[1]]
			re = re[loc[1]:]
			if match[0] == '\\' && loc[2] >= 0 {
				n, _ := strconv.Atoi(match[1:])
				out.WriteString(`\` + strconv.Itoa(n+offset))
			} else {
				out.WriteString(match)
				if match == "(" {
					numCaptures++
				}
			}
		}
		parts[i] = "(" + out.String() + ")"
	}
	return strings.Join(parts, "|")
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
	if parent == nil || mode.beginKeywords == "" {
		return
	}
	mode.begin = stringPattern(`\b(` + strings.Join(strings.Split(mode.beginKeywords, " "), "|") + `)(?!\.)(?=\b|\s)`)
	mode.present |= keyBegin
	mode.beforeBegin = skipIfhasPrecedingDot
	mode.present |= keyBeforeBegin
	if !mode.keywords.truthy() {
		mode.keywords = keywordsValue{kind: keywordsKindString, text: mode.beginKeywords}
	}
	mode.present |= keyKeywords
	deleteKey(mode, keyBeginKeywords)
	if !mode.hasRelevance {
		mode.relevance, mode.hasRelevance = 0, true
		mode.present |= keyRelevance
	}
}

func extCompileIllegal(mode, _ *Mode) {
	if mode.illegal.kind != patternList {
		return
	}
	mode.illegal = stringPattern(either(mode.illegal.list))
}

func extCompileMatch(mode, _ *Mode) {
	if !mode.match.truthy() {
		return
	}
	if mode.begin.truthy() || mode.end.truthy() {
		throw("begin & end are not supported with match")
	}
	mode.begin = mode.match
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
	if language.present&keyCompilerExtensions == 0 || language.compilerExtensions == nil {
		language.compilerExtensions = []compilerExtension{}
		language.present |= keyCompilerExtensions
	}
	for _, c := range language.contains {
		if c.self {
			throw("ERR: contains `self` is not supported at the top-level of a language.  See documentation.")
		}
	}
	aliases := make(map[string]string, len(language.classNameAliases))
	maps.Copy(aliases, language.classNameAliases)
	language.classNameAliases = aliases
	language.present |= keyClassNameAliases
	return compileMode(language, language, nil)
}

func compileMode(language, mode, parent *Mode) *Mode {
	if mode.isCompiled {
		return mode
	}
	extCompileMatch(mode, parent)
	for _, ext := range language.compilerExtensions {
		ext(mode, parent)
	}
	mode.beforeBegin = nil
	mode.present |= keyBeforeBegin
	extBeginKeywords(mode, parent)
	extCompileIllegal(mode, parent)
	extCompileRelevance(mode, parent)
	mode.isCompiled = true
	mode.present |= keyIsCompiled

	var keywordPattern pattern
	if mode.keywords.kind == keywordsKindObject {
		object := mode.keywords.object
		if object.hasPattern {
			keywordPattern = object.pattern
		}
		object.pattern, object.hasPattern = pattern{}, false
	}
	if mode.keywords.truthy() {
		mode.keywords = keywordsValue{kind: keywordsKindCompiled, compiled: compileKeywordsValue(mode.keywords, language.caseInsensitive)}
	}
	if mode.lexemes.truthy() && keywordPattern.truthy() {
		throw("ERR: Prefer `keywords.$pattern` to `mode.lexemes`, BOTH are not allowed. (see mode reference) ")
	}
	switch {
	case keywordPattern.truthy():
	case mode.lexemes.truthy():
		keywordPattern = mode.lexemes
	default:
		keywordPattern = regExpPattern(`\w+`)
	}
	mode.keywordPatternRe = langRe(language, keywordPattern)
	mode.present |= keyKeywordPatternRe

	if parent != nil {
		if !mode.begin.truthy() {
			mode.begin = regExpPattern(`\B|\b`)
			mode.present |= keyBegin
		}
		mode.beginRe = langRe(language, mode.begin)
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
			mode.endRe = langRe(language, mode.end)
			mode.present |= keyEndRe
		}
		mode.terminatorEnd, _ = mode.end.source()
		mode.present |= keyTerminatorEnd
		if mode.endsWithParent && parent.terminatorEnd != "" {
			if mode.end.truthy() {
				mode.terminatorEnd += "|"
			}
			mode.terminatorEnd += parent.terminatorEnd
		}
	}
	if mode.illegal.truthy() {
		mode.illegalRe = langRe(language, mode.illegal)
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
	mode.matcher = buildModeRegex(language, mode)
	mode.present |= keyMatcher
	return mode
}

func buildModeRegex(language, mode *Mode) *resumableMultiRegex {
	mm := &resumableMultiRegex{multiRegexes: map[int]*multiRegex{}, language: language}
	for _, term := range mode.contains {
		mm.addRule(term.mode.begin, &matchOptions{rule: term.mode, typ: "begin"})
	}
	if mode.terminatorEnd != "" {
		mm.addRule(stringPattern(mode.terminatorEnd), &matchOptions{typ: "end"})
	}
	if mode.illegal.truthy() {
		mm.addRule(mode.illegal, &matchOptions{typ: "illegal"})
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
		mode.cachedVariants, mode.hasCachedVariants = variants, true
		mode.present |= keyCachedVariants
	}
	if mode.hasCachedVariants {
		return mode.cachedVariants
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
}

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
	lastMatch      *enhancedMatch
}

// highlight is highlight.js _highlight(languageName, codeToHighlight, ignoreIllegals, continuation).
func (r *Registry) highlight(languageName string, code []rune, ignoreIllegals bool, continuation *frame) Result {
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
	}
	run.top = continuation
	if run.top == nil {
		run.top = languageFrame(md)
	}
	run.processContinuations()
	return run.run()
}

func languageFrame(language *Mode) *frame {
	if language.rootFrame == nil {
		language.rootFrame = &frame{mode: language, language: true}
	}
	return language.rootFrame
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
			result = Result{Illegal: true, Value: escapeHTML(unitsToString(h.code)), emitter: h.emitter}
			return
		}
		result = Result{Value: escapeHTML(unitsToString(h.code)), Language: h.languageName, HasLanguage: true, emitter: h.emitter, top: h.top}
	}()
	h.top.mode.matcher.considerAll()
	for {
		h.iterations++
		if h.resume {
			h.resume = false
		} else {
			h.top.mode.matcher.considerAll()
		}
		h.top.mode.matcher.lastIndex = h.index
		match := h.top.mode.matcher.exec(h.code)
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
	}
}

func (h *highlightRun) keywordData(mode *Mode, lexeme []rune) (keywordData, bool) {
	matchText := unitsToString(lexeme)
	if h.language.caseInsensitive {
		matchText = toLowerCase(matchText)
	}
	data, ok := mode.keywords.compiled[matchText]
	return data, ok
}

func (h *highlightRun) processKeywords() {
	top := h.top.mode
	if !top.keywords.truthy() {
		h.emitter.addText(unitsToString(h.modeBuffer))
		return
	}
	lastIndex := 0
	match := top.keywordPatternRe.exec(h.modeBuffer, 0)
	var buf []rune
	for match != nil {
		buf = append(buf, substring(h.modeBuffer, lastIndex, match.index)...)
		text := h.modeBuffer[match.spans[0]:match.spans[1]]
		if data, ok := h.keywordData(top, text); ok {
			h.emitter.addText(unitsToString(buf))
			buf = nil
			h.relevance += data.relevance
			if strings.HasPrefix(data.kind, "_") {
				buf = append(buf, text...)
			} else {
				cssClass := h.language.classNameAliases[data.kind]
				if cssClass == "" {
					cssClass = data.kind
				}
				h.emitter.addKeyword(unitsToString(text), cssClass)
			}
		} else {
			buf = append(buf, text...)
		}
		lastIndex = match.spans[1]
		match = top.keywordPatternRe.exec(h.modeBuffer, lastIndex)
	}
	buf = append(buf, substr(h.modeBuffer, lastIndex)...)
	h.emitter.addText(unitsToString(buf))
}

func (h *highlightRun) processSubLanguage() {
	if len(h.modeBuffer) == 0 {
		return
	}
	top := h.top.mode
	var result Result
	if !top.subLanguage.isList {
		if h.registry.languages[top.subLanguage.name] == nil {
			h.emitter.addText(unitsToString(h.modeBuffer))
			return
		}
		result = h.registry.highlight(top.subLanguage.name, h.modeBuffer, true, h.continuations[top.subLanguage.name])
		h.continuations[top.subLanguage.name] = result.top
	} else {
		var subset []string
		if len(top.subLanguage.list) > 0 {
			subset = top.subLanguage.list
		}
		result = h.registry.highlightAuto(h.modeBuffer, subset)
	}
	if top.relevance > 0 {
		h.relevance += result.Relevance
	}
	h.emitter.addSublanguage(result.emitter, result.Language)
}

func (h *highlightRun) processBuffer() {
	if h.top.mode.subLanguage.set {
		h.processSubLanguage()
	} else {
		h.processKeywords()
	}
	h.modeBuffer = nil
}

func (h *highlightRun) startNewMode(mode *Mode) *frame {
	if mode.className != "" {
		className := h.language.classNameAliases[mode.className]
		if className == "" {
			className = mode.className
		}
		h.emitter.openNode(className)
	}
	h.top = &frame{mode: mode, parent: h.top}
	return h.top
}

func (h *highlightRun) endOfMode(mode *frame, match *enhancedMatch, matchPlusRemainder []rune) *frame {
	matched := mode.mode.endRe != nil && mode.mode.endRe.matchesAtStart(matchPlusRemainder)
	if matched {
		if mode.mode.onEnd != nil {
			resp := mode.response()
			mode.mode.onEnd(match, resp)
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
	if h.top.mode.matcher.regexIndex == 0 {
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
	resp := newRuleResponse(newMode)
	for _, cb := range []callback{newMode.beforeBegin, newMode.onBegin} {
		if cb == nil {
			continue
		}
		cb(match, resp)
		if resp.isMatchIgnored {
			return h.doIgnore(lexeme)
		}
	}
	if newMode.endSameAsBegin {
		newMode.endRe = escapeRegExp(unitsToString(lexeme))
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
			h.modeBuffer = append([]rune(nil), lexeme...)
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
			h.modeBuffer = append([]rune(nil), lexeme...)
		}
	}
	for {
		if h.top.mode.className != "" {
			h.emitter.closeNode()
		}
		if !h.top.mode.skip && !h.top.mode.subLanguage.truthy() {
			h.relevance += h.top.mode.relevance
		}
		h.top = h.top.parent
		if h.top == endMode.parent {
			break
		}
	}
	if endMode.mode.starts != nil {
		if endMode.mode.endSameAsBegin {
			endMode.mode.starts.endRe = endMode.mode.endRe
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
	if h.lastMatch != nil && h.lastMatch.matchType() == "begin" && match.matchType() == "end" && h.lastMatch.index == match.index && len(lexeme) == 0 {
		h.modeBuffer = append(h.modeBuffer, slice(h.code, match.index, match.index+1)...)
		return 1
	}
	h.lastMatch = match
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
	if h.iterations > 100000 && h.iterations > match.index*3 {
		throw("potential infinite loop, way more iterations than matches")
	}
	h.modeBuffer = append(h.modeBuffer, lexeme...)
	return len(lexeme)
}

// highlightAuto is highlight.js highlightAuto(code, languageSubset).
func (r *Registry) highlightAuto(code []rune, languageSubset []string) Result {
	if languageSubset == nil {
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
			if r.getLanguage(a.Language).supersetOf == b.Language {
				return 1
			}
			if r.getLanguage(b.Language).supersetOf == a.Language {
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
func stringToUnits(text string) []rune {
	units := make([]rune, 0, len(text))
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
