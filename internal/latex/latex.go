// Package latex renders a basic subset of LaTeX math as terminal-friendly
// Unicode text. It is a faithful port of upstream pi's tui/src/latex.ts:
// renderLatex returns the rendered string and true, or ("", false) when the
// expression contains unsupported or malformed syntax (pi's `undefined`).
//
// The static lookup tables live in symbols.go, generated from the pinned
// upstream source by internal/latex/gentables.
package latex

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Sentinels mirror latex.ts. Display mode emits fraction/operator placeholders
// (marker START + node index + marker END) that renderLayout expands into 2D
// art; PROTECTED_SPACE guards alignment padding from the final space collapse;
// NEGATIVE_SPACE marks a trailing-trim request from \! and friends.
const (
	layoutMarkerStart = "\U000f0000"
	layoutMarkerEnd   = "\U000f0001"
	protectedSpace    = "\u00a0"
	negativeSpace     = "\u0000"
)

var (
	reSimpleLetterNumber = regexp.MustCompile(`^[\p{L}\p{N}.]+$`)
	reSimpleNumber       = regexp.MustCompile(`^[\p{N}.]+$`)
	reASCIILetters       = regexp.MustCompile(`^[A-Za-z]+$`)
	reSpaceTabRun        = regexp.MustCompile(`[ \t]+`)
	reCasesKeyword       = regexp.MustCompile(`(?i)^(?:if|when|for|otherwise)\b`)
	reTrailingComma      = regexp.MustCompile(`,\s*$`)
	reEnvRowSplit        = regexp.MustCompile(`\\\\(?:\[[^\]\n]*\])?`)
	reArrayColumnSpec    = regexp.MustCompile(`^\s*\{[^}]*\}`)
	reLayoutMarker       = regexp.MustCompile(`\x{f0000}(\d+)\x{f0001}`)
)

// RenderLatexOptions mirrors upstream RenderLatexOptions.
type RenderLatexOptions struct {
	// Display stacks fractions and operator limits vertically (default: false).
	Display bool
}

// RenderLatex renders a LaTeX math expression to Unicode text. The bool is
// false (upstream `undefined`) for unsupported or malformed input.
func RenderLatex(source string, opts RenderLatexOptions) (string, bool) {
	var nodes *[]layoutNode
	if opts.Display {
		n := []layoutNode{}
		nodes = &n
	}
	p := &parser{src: []rune(source), nodes: nodes, ok: true, stack: true}
	rendered, ok := p.render()
	if !ok {
		return "", false
	}
	if nodes == nil || len(*nodes) == 0 {
		return strings.ReplaceAll(rendered, protectedSpace, " "), true
	}
	lines := renderLayout(rendered, *nodes).lines

	indentation := -1
	for _, line := range lines {
		if trimJS(line) == "" {
			continue
		}
		lead := utf8.RuneCountInString(line) - utf8.RuneCountInString(trimStartJS(line))
		if indentation < 0 || lead < indentation {
			indentation = lead
		}
	}
	if indentation < 0 {
		indentation = 0
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = trimEndJS(sliceRunesFrom(line, indentation))
	}
	return strings.ReplaceAll(trimEndJS(strings.Join(out, "\n")), protectedSpace, " "), true
}

// --- string helpers (faithful to the TS regex/format helpers) ---

func replaceCharacters(value string, table map[string]string) (string, bool) {
	var b strings.Builder
	for _, r := range value {
		rep, ok := table[string(r)]
		if !ok {
			return "", false
		}
		b.WriteString(rep)
	}
	return b.String(), true
}

func formatScript(value, kind string) string {
	value = trimJS(value)
	table := latexSuperscripts
	prefix := "^"
	if kind == "sub" {
		table = latexSubscripts
		prefix = "_"
	}
	if unicode, ok := replaceCharacters(value, table); ok {
		return unicode
	}
	if utf8.RuneCountInString(value) == 1 || (kind == "sub" && reASCIILetters.MatchString(value)) {
		return prefix + value
	}
	return prefix + "(" + value + ")"
}

func formatFraction(numerator, denominator string) string {
	numerator = trimJS(numerator)
	denominator = trimJS(denominator)
	num := numerator
	if !reSimpleLetterNumber.MatchString(numerator) {
		num = "(" + numerator + ")"
	}
	den := denominator
	if !reSimpleNumber.MatchString(denominator) && utf8.RuneCountInString(denominator) != 1 {
		den = "(" + denominator + ")"
	}
	return num + "/" + den
}

func formatRoot(value, symbol string) string {
	value = trimJS(value)
	if reSimpleLetterNumber.MatchString(value) {
		return symbol + value
	}
	return symbol + "(" + value + ")"
}

func normalizeOutput(value string) string {
	lines := strings.Split(value, "\n")
	mapped := make([]string, len(lines))
	for i, line := range lines {
		mapped[i] = trimJS(reSpaceTabRun.ReplaceAllString(line, " "))
	}
	var kept []string
	for i, line := range mapped {
		if line != "" || (i > 0 && i < len(mapped)-1) {
			kept = append(kept, line)
		}
	}
	return trimJS(strings.Join(kept, "\n"))
}

// --- 2D layout for display mode ---

type layoutNode struct {
	kind         string // "fraction" | "operator"
	numerator    string
	denominator  string
	operator     string
	lower, upper *string
}

type layout struct {
	lines    []string
	width    int
	baseline int
}

func padLayoutLine(line string, width int, centered bool) string {
	padding := max(width-visibleWidth(line), 0)
	left := 0
	if centered {
		left = padding / 2
	}
	return strings.Repeat(" ", left) + line + strings.Repeat(" ", padding-left)
}

func joinLayouts(layouts []layout) layout {
	if len(layouts) == 0 {
		return layout{lines: []string{""}, width: 0, baseline: 0}
	}
	baseline := 0
	below := 0
	for _, l := range layouts {
		if l.baseline > baseline {
			baseline = l.baseline
		}
		if b := len(l.lines) - l.baseline - 1; b > below {
			below = b
		}
	}
	var lines []string
	for row := 0; row <= baseline+below; row++ {
		var sb strings.Builder
		for _, l := range layouts {
			sourceRow := row - baseline + l.baseline
			if sourceRow >= 0 && sourceRow < len(l.lines) {
				sb.WriteString(padLayoutLine(l.lines[sourceRow], l.width, false))
			} else {
				sb.WriteString(strings.Repeat(" ", l.width))
			}
		}
		lines = append(lines, trimEndJS(sb.String()))
	}
	width := 0
	for _, l := range layouts {
		width += l.width
	}
	return layout{lines: lines, width: width, baseline: baseline}
}

func renderLayout(source string, nodes []layoutNode) layout {
	var renderedLines []string
	firstBaseline := 0
	for sourceLine := range strings.SplitSeq(source, "\n") {
		var layouts []layout
		position := 0
		previousWasNode := false
		for _, m := range reLayoutMarker.FindAllStringSubmatchIndex(sourceLine, -1) {
			index := m[0]
			if index > position {
				sliced := sourceLine[position:index]
				if previousWasNode {
					sliced = trimStartJS(sliced)
				}
				text := trimEndJS(sliced)
				layouts = append(layouts, layout{lines: []string{text}, width: visibleWidth(text), baseline: 0})
			}
			idx, _ := strconv.Atoi(sourceLine[m[2]:m[3]])
			if idx >= 0 && idx < len(nodes) {
				layouts = append(layouts, nodeLayout(nodes[idx], nodes))
			}
			position = m[1]
			previousWasNode = true
		}
		if position < len(sourceLine) {
			sliced := sourceLine[position:]
			if previousWasNode {
				sliced = trimStartJS(sliced)
			}
			layouts = append(layouts, layout{lines: []string{sliced}, width: visibleWidth(sliced), baseline: 0})
		}
		lineLayout := joinLayouts(layouts)
		if len(renderedLines) == 0 {
			firstBaseline = lineLayout.baseline
		}
		renderedLines = append(renderedLines, lineLayout.lines...)
	}
	width := 0
	for _, line := range renderedLines {
		if w := visibleWidth(line); w > width {
			width = w
		}
	}
	return layout{lines: renderedLines, width: width, baseline: firstBaseline}
}

func nodeLayout(node layoutNode, nodes []layoutNode) layout {
	if node.kind == "fraction" {
		numerator := renderLayout(node.numerator, nodes)
		denominator := renderLayout(node.denominator, nodes)
		contentWidth := max(max(numerator.width, denominator.width), 1)
		width := contentWidth + 2
		lines := make([]string, 0, len(numerator.lines)+1+len(denominator.lines))
		for _, l := range numerator.lines {
			lines = append(lines, padLayoutLine(l, width, true))
		}
		lines = append(lines, " "+strings.Repeat("─", contentWidth)+" ")
		for _, l := range denominator.lines {
			lines = append(lines, padLayoutLine(l, width, true))
		}
		return layout{lines: lines, width: width, baseline: len(numerator.lines)}
	}
	contentWidth := visibleWidth(node.operator)
	if node.lower != nil {
		contentWidth = max(contentWidth, visibleWidth(*node.lower))
	}
	if node.upper != nil {
		contentWidth = max(contentWidth, visibleWidth(*node.upper))
	}
	var lines []string
	if node.upper != nil {
		lines = append(lines, padLayoutLine(*node.upper, contentWidth, true)+" ")
	}
	lines = append(lines, padLayoutLine(node.operator, contentWidth, true)+" ")
	if node.lower != nil {
		lines = append(lines, padLayoutLine(*node.lower, contentWidth, true)+" ")
	}
	baseline := 0
	if node.upper != nil {
		baseline = 1
	}
	return layout{lines: lines, width: contentWidth + 1, baseline: baseline}
}

// --- parser ---

type parser struct {
	src   []rune
	nodes *[]layoutNode // nil = inline mode (no display stacking)
	pos   int
	ok    bool
	stack bool
}

func (p *parser) at(i int) rune {
	if i < 0 || i >= len(p.src) {
		return -1
	}
	return p.src[i]
}

func (p *parser) render() (string, bool) {
	rendered := p.parseSequence(0)
	if !p.ok || p.pos != len(p.src) {
		return "", false
	}
	return normalizeOutput(rendered), true
}

func (p *parser) parseSequence(end rune) string {
	var result string
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if end != 0 && c == end {
			p.pos++
			return result
		}
		if c == '}' {
			p.ok = false
			return result
		}
		if c == '{' {
			p.pos++
			result += p.parseSequence('}')
			continue
		}
		if c == '\\' {
			command := p.parseCommand()
			if command == negativeSpace {
				result = trimEndJS(result)
			} else {
				result += command
			}
			continue
		}
		if c == '^' || c == '_' {
			p.pos++
			result = trimEndJS(result)
			kind := "sup"
			if c == '_' {
				kind = "sub"
			}
			result += formatScript(p.parseRequiredArgument(false), kind)
			continue
		}
		if isJSSpace(c) {
			result += p.parseWhitespace()
			continue
		}
		if c == '&' {
			p.pos++
			continue
		}
		if c == '~' {
			p.pos++
			result += " "
			continue
		}
		result += string(c)
		p.pos++
	}
	if end != 0 {
		p.ok = false
	}
	return result
}

func (p *parser) parseWhitespace() string {
	for p.pos < len(p.src) && isJSSpace(p.src[p.pos]) {
		p.pos++
	}
	return " "
}

func (p *parser) parseCommand() string {
	p.pos++
	if p.pos >= len(p.src) {
		p.ok = false
		return ""
	}
	var command string
	first := p.src[p.pos]
	if isASCIILetter(first) {
		start := p.pos
		for p.pos < len(p.src) && isASCIILetter(p.src[p.pos]) {
			p.pos++
		}
		command = string(p.src[start:p.pos])
	} else {
		command = string(first)
		p.pos++
	}

	switch {
	case command == "\\":
		return "\n"
	case latexSpacingCommands[command]:
		return " "
	case latexNegativeSpacingCommands[command]:
		return negativeSpace
	case latexIgnoredCommands[command]:
		return ""
	case command == "{" || command == "}" || command == "$" || command == "%" || command == "#" || command == "_" || command == "&":
		return command
	case command == "|":
		return "‖"
	case command == "not":
		value := trimJS(p.parseRequiredArgument(false))
		if negated, ok := latexNegatedSymbols[value]; ok {
			return negated
		}
		chars := []rune(value)
		if len(chars) == 0 {
			p.ok = false
			return ""
		}
		return string(chars[0]) + "\u0338" + string(chars[1:])
	case latexLimitOperators[command]:
		return p.parseOperator(command, "bracket", true, true)
	}

	if symbol, ok := latexSymbols[command]; ok {
		if latexDisplayLimitSymbols[command] {
			return p.parseOperator(symbol, "script", true, false)
		}
		return symbol
	}
	if latexNamedOperators[command] {
		return " " + command + " "
	}
	if latexSizeCommands[command] {
		return ""
	}
	if command == "left" || command == "middle" || command == "right" {
		if p.at(p.pos) == '.' {
			p.pos++
		}
		return ""
	}
	if command == "frac" || command == "dfrac" || command == "tfrac" {
		shouldStack := p.nodes != nil && p.stack && command != "tfrac"
		numerator := p.parseRequiredArgument(!shouldStack)
		denominator := p.parseRequiredArgument(!shouldStack)
		if shouldStack {
			*p.nodes = append(*p.nodes, layoutNode{
				kind:        "fraction",
				numerator:   normalizeOutput(numerator),
				denominator: normalizeOutput(denominator),
			})
			return layoutMarkerStart + strconv.Itoa(len(*p.nodes)-1) + layoutMarkerEnd
		}
		return formatFraction(numerator, denominator)
	}
	if command == "sqrt" {
		degree := p.parseOptionalArgument()
		value := p.parseRequiredArgument(true)
		if degree == nil || trimJS(*degree) == "2" {
			return formatRoot(value, "√")
		}
		switch trimJS(*degree) {
		case "3":
			return formatRoot(value, "∛")
		case "4":
			return formatRoot(value, "∜")
		}
		return formatScript(trimJS(*degree), "sup") + formatRoot(value, "√")
	}
	if command == "boxed" || command == "fbox" {
		return "[" + trimJS(p.parseRequiredArgument(true)) + "]"
	}
	if command == "binom" || command == "dbinom" || command == "tbinom" {
		return "(" + p.parseRequiredArgument(true) + " choose " + p.parseRequiredArgument(true) + ")"
	}
	if accent, ok := latexAccents[command]; ok {
		value := p.parseRequiredArgument(true)
		if utf8.RuneCountInString(value) == 1 {
			return value + accent
		}
		return command + "(" + value + ")"
	}
	if command == "mathbb" {
		value := p.parseRequiredArgument(true)
		var b strings.Builder
		for _, r := range value {
			if bb, ok := latexBlackboard[string(r)]; ok {
				b.WriteString(bb)
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	if command == "operatorname" {
		starred := p.at(p.pos) == '*'
		if starred {
			p.pos++
		}
		operator := trimJS(normalizeOutput(p.parseRequiredArgument(true)))
		return p.parseOperator(operator, "bracket", starred, true)
	}
	if command == "mod" || command == "bmod" {
		return " mod "
	}
	if command == "pmod" || command == "pod" {
		value := trimJS(p.parseRequiredArgument(true))
		if command == "pmod" {
			return " (mod " + value + ")"
		}
		return " (" + value + ")"
	}
	if command == "overset" || command == "stackrel" {
		upper := p.parseRequiredArgument(true)
		value := trimJS(p.parseRequiredArgument(true))
		return value + formatScript(upper, "sup")
	}
	if command == "underset" {
		lower := p.parseRequiredArgument(true)
		value := trimJS(p.parseRequiredArgument(true))
		return value + formatScript(lower, "sub")
	}
	if latexPlainWrappers[command] {
		value := p.parseRequiredArgument(true)
		if strings.HasPrefix(command, "text") || command == "mbox" {
			return value
		}
		return trimJS(value)
	}
	if command == "begin" {
		return p.parseEnvironment()
	}
	if command == "end" {
		p.ok = false
		return ""
	}

	p.ok = false
	return "\\" + command
}

func (p *parser) parseOperator(operator, inlineLowerStyle string, displayLimits, spaced bool) string {
	useDisplayLimits := displayLimits
	modifierPosition := p.pos
	for modifierPosition < len(p.src) && (p.src[modifierPosition] == ' ' || p.src[modifierPosition] == '\t') {
		modifierPosition++
	}
	if isLimits, matchLen, matched := p.matchLimitsModifier(modifierPosition); matched {
		useDisplayLimits = isLimits
		p.pos = modifierPosition + matchLen
	}

	var lower, upper *string
	for {
		scriptPosition := p.pos
		for scriptPosition < len(p.src) && (p.src[scriptPosition] == ' ' || p.src[scriptPosition] == '\t') {
			scriptPosition++
		}
		kind := p.at(scriptPosition)
		if kind != '_' && kind != '^' {
			break
		}
		p.pos = scriptPosition + 1
		value := strings.ReplaceAll(normalizeOutput(p.parseRequiredArgument(false)), " ", "")
		if kind == '_' {
			if lower != nil {
				p.ok = false
			}
			v := value
			lower = &v
		} else {
			if upper != nil {
				p.ok = false
			}
			v := value
			upper = &v
		}
	}

	if p.nodes != nil && useDisplayLimits && (lower != nil || upper != nil) {
		*p.nodes = append(*p.nodes, layoutNode{kind: "operator", operator: operator, lower: lower, upper: upper})
		return layoutMarkerStart + strconv.Itoa(len(*p.nodes)-1) + layoutMarkerEnd
	}

	rendered := operator
	if lower != nil {
		if inlineLowerStyle == "bracket" {
			rendered += "[" + *lower + "]"
		} else {
			rendered += formatScript(*lower, "sub")
		}
	}
	if upper != nil {
		rendered += formatScript(*upper, "sup")
	}
	if spaced {
		return " " + rendered + " "
	}
	return rendered
}

// matchLimitsModifier ports /^\\(limits|nolimits)(?![A-Za-z])/ without lookahead.
func (p *parser) matchLimitsModifier(from int) (isLimits bool, matchLen int, matched bool) {
	for _, kw := range []struct {
		name   string
		limits bool
	}{{"\\nolimits", false}, {"\\limits", true}} {
		r := []rune(kw.name)
		if from+len(r) > len(p.src) {
			continue
		}
		if !runesHavePrefix(p.src[from:], r) {
			continue
		}
		if !isASCIILetter(p.at(from + len(r))) {
			return kw.limits, len(r), true
		}
	}
	return false, 0, false
}

func (p *parser) parseRequiredArgument(stackFractions bool) string {
	previous := p.stack
	p.stack = previous && stackFractions
	value := p.parseRequiredArgumentValue()
	p.stack = previous
	return value
}

func (p *parser) parseRequiredArgumentValue() string {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if p.pos >= len(p.src) {
		p.ok = false
		return ""
	}
	if p.src[p.pos] == '{' {
		p.pos++
		return p.parseSequence('}')
	}
	if p.src[p.pos] == '\\' {
		return p.parseCommand()
	}
	value := string(p.src[p.pos])
	p.pos++
	return value
}

func (p *parser) parseOptionalArgument() *string {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if p.at(p.pos) != '[' {
		return nil
	}
	end := indexOfRune(p.src, "]", p.pos+1)
	if end < 0 {
		p.ok = false
		return nil
	}
	value := string(p.src[p.pos+1 : end])
	p.pos = end + 1
	rendered := p.renderNested(value, true)
	return &rendered
}

func (p *parser) readRawGroup() (string, bool) {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if p.at(p.pos) != '{' {
		p.ok = false
		return "", false
	}
	p.pos++
	start := p.pos
	depth := 1
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\\' {
			p.pos += 2
			continue
		}
		if c == '{' {
			depth++
		}
		if c == '}' {
			depth--
		}
		if depth == 0 {
			value := string(p.src[start:p.pos])
			p.pos++
			return value, true
		}
		p.pos++
	}
	p.ok = false
	return "", false
}

func (p *parser) splitEnvironmentRows(body string) []string {
	return reEnvRowSplit.Split(body, -1)
}

func (p *parser) parseEnvironment() string {
	environment, ok := p.readRawGroup()
	if !ok {
		return ""
	}
	endMarker := "\\end{" + environment + "}"
	end := indexOfRune(p.src, endMarker, p.pos)
	if end < 0 {
		p.ok = false
		return ""
	}
	body := string(p.src[p.pos:end])
	p.pos = end + utf8.RuneCountInString(endMarker)

	switch environment {
	case "equation", "equation*", "displaymath":
		return trimJS(p.renderNested(body, true))
	case "aligned", "align", "align*", "alignedat", "alignat", "alignat*",
		"gather", "gathered", "multline", "multline*", "split":
		alignedAt := environment == "alignedat" || environment == "alignat" || environment == "alignat*"
		alignedBody := body
		if alignedAt {
			alignedBody = reArrayColumnSpec.ReplaceAllString(body, "")
		}
		var out []string
		for _, row := range p.splitEnvironmentRows(alignedBody) {
			cells := strings.Split(row, "&")
			var source string
			if alignedAt {
				var parts []string
				for i := 0; i < len(cells); i += 2 {
					end := min(i+2, len(cells))
					parts = append(parts, strings.Join(cells[i:end], ""))
				}
				source = strings.Join(parts, " ")
			} else {
				source = strings.Join(cells, "")
			}
			if rendered := trimJS(p.renderNested(source, true)); rendered != "" {
				out = append(out, rendered)
			}
		}
		return strings.Join(out, "\n")
	case "cases", "cases*":
		var rows [][]string
		for _, row := range p.splitEnvironmentRows(body) {
			var cells []string
			any := false
			for cell := range strings.SplitSeq(row, "&") {
				c := trimJS(p.renderNested(cell, false))
				cells = append(cells, c)
				if c != "" {
					any = true
				}
			}
			if any {
				rows = append(rows, cells)
			}
		}
		var out []string
		for index, row := range rows {
			value := ""
			if len(row) > 0 {
				value = reTrailingComma.ReplaceAllString(row[0], "")
			}
			condition := ""
			if len(row) > 1 {
				condition = row[1]
			}
			delimiter := "⎨"
			if index == 0 {
				delimiter = "⎧"
			} else if index == len(rows)-1 {
				delimiter = "⎩"
			}
			line := delimiter + " " + value
			if condition != "" {
				conditionPrefix := " if "
				if reCasesKeyword.MatchString(condition) {
					conditionPrefix = " "
				}
				line += conditionPrefix + condition
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	case "array", "matrix", "smallmatrix", "pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix":
		matrixBody := body
		if environment == "array" {
			matrixBody = reArrayColumnSpec.ReplaceAllString(body, "")
		}
		return p.renderMatrix(environment, matrixBody)
	}

	p.ok = false
	return body
}

func (p *parser) renderMatrix(environment, body string) string {
	var matrix [][]string
	for _, row := range p.splitEnvironmentRows(body) {
		var cells []string
		any := false
		for cell := range strings.SplitSeq(row, "&") {
			c := trimJS(p.renderNested(cell, false))
			cells = append(cells, c)
			if c != "" {
				any = true
			}
		}
		if any {
			matrix = append(matrix, cells)
		}
	}
	columnCount := 0
	for _, row := range matrix {
		columnCount = max(columnCount, len(row))
	}
	columnWidths := make([]int, columnCount)
	for c := 0; c < columnCount; c++ {
		for _, row := range matrix {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			columnWidths[c] = max(columnWidths[c], visibleWidth(cell))
		}
	}
	rows := make([]string, len(matrix))
	for i, row := range matrix {
		cells := make([]string, columnCount)
		for c := 0; c < columnCount; c++ {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			cells[c] = cell + strings.Repeat(protectedSpace, max(0, columnWidths[c]-visibleWidth(cell)))
		}
		rows[i] = strings.Join(cells, " │ ")
	}
	if environment == "array" || environment == "matrix" || environment == "smallmatrix" {
		return strings.Join(rows, "\n")
	}

	delimiters := map[string][6]string{
		"pmatrix": {"⎛", "⎞", "⎜", "⎟", "⎝", "⎠"},
		"bmatrix": {"⎡", "⎤", "⎢", "⎥", "⎣", "⎦"},
		"Bmatrix": {"⎧", "⎫", "⎨", "⎬", "⎩", "⎭"},
		"vmatrix": {"│", "│", "│", "│", "│", "│"},
		"Vmatrix": {"║", "║", "║", "║", "║", "║"},
	}
	delimiter, ok := delimiters[environment]
	if !ok {
		p.ok = false
		return strings.Join(rows, "\n")
	}
	if len(rows) == 1 {
		return delimiter[0] + " " + rows[0] + " " + delimiter[1]
	}
	out := make([]string, len(rows))
	for index, row := range rows {
		left := delimiter[2]
		right := delimiter[3]
		if index == 0 {
			left, right = delimiter[0], delimiter[1]
		} else if index == len(rows)-1 {
			left, right = delimiter[4], delimiter[5]
		}
		out[index] = left + " " + row + " " + right
	}
	return strings.Join(out, "\n")
}

func (p *parser) renderNested(source string, stackFractions bool) string {
	var nodes *[]layoutNode
	if stackFractions {
		nodes = p.nodes
	}
	sub := &parser{src: []rune(source), nodes: nodes, ok: true, stack: true}
	rendered, ok := sub.render()
	if !ok {
		p.ok = false
		return source
	}
	return rendered
}
