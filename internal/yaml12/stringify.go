package yaml12

// Ports the flow-style part of yaml 2.9.0 src/stringify/{stringify,stringifyCollection,stringifyPair,stringifyString,stringifyNumber,stringifyComment,foldFlowLines}.ts that the library runs when toJS names a mapping key that is a collection: `key.toString(ctx)` with `inFlow` set. Block collections and block scalars cannot occur there (a block scalar in a flow context is quoted; a plain scalar at indent 0 cannot occur). Copyright Eemeli Aro, ISC licence (see lexer.go).

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// stringifyError carries an error the library throws while stringifying.
type stringifyError struct{ message string }

func throw(message string) { panic(stringifyError{message}) }

const (
	lineWidth       = 80
	minContentWidth = 20
	indentStep      = "  "
	foldFlow        = "flow"
	foldQuoted      = "quoted"
)

type strCtx struct {
	anchors          map[string]bool
	pushed           map[string]bool
	indent           string
	inFlow           bool
	implicitKey      bool
	allNullValues    bool
	actualString     bool
	indentAtStart    int
	hasIndentAtStart bool
}

func (c *strCtx) clone() *strCtx { d := *c; return &d }

func u16(s string) []uint16 { return utf16.Encode([]rune(s)) }

func fromU16(u []uint16) string { return string(utf16.Decode(u)) }

func u16len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// hasDocumentMarker is /^(%|---|\.\.\.)/m.
func hasDocumentMarker(s string) bool {
	rs := []rune(s)
	for i := 0; i <= len(rs); i++ {
		if i != 0 && !isLineTerminator(rs[i-1]) {
			continue
		}
		rest := string(rs[i:])
		if strings.HasPrefix(rest, "%") || strings.HasPrefix(rest, "---") || strings.HasPrefix(rest, "...") {
			return true
		}
	}
	return false
}

// commentString is stringifyComment: each non-empty line gets a leading #.
func commentString(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := range rs {
		if i == 0 || isLineTerminator(rs[i-1]) {
			atEOL := func(j int) bool { return j >= len(rs) || isLineTerminator(rs[j]) }
			if !atEOL(i) {
				b.WriteByte('#')
				if rs[i] == ' ' && atEOL(i+1) {
					continue
				}
			}
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

func allNewlines(s string) bool { return s != "" && strings.Trim(s, "\n") == "" }

// indentComment is the library's helper of the same name.
func indentComment(comment, indent string) string {
	if allNewlines(comment) {
		return comment[1:]
	}
	if indent == "" {
		return comment
	}
	rs := []rune(comment)
	var b strings.Builder
	for i := range rs {
		if i == 0 || isLineTerminator(rs[i-1]) {
			j := i
			for j < len(rs) && rs[j] == ' ' {
				j++
			}
			if j < len(rs) && !isLineTerminator(rs[j]) {
				b.WriteString(indent)
			}
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

func lineComment(str, indent, comment string) string {
	switch {
	case strings.HasSuffix(str, "\n"):
		return indentComment(comment, indent)
	case strings.Contains(comment, "\n"):
		return "\n" + indentComment(comment, indent)
	case strings.HasSuffix(str, " "):
		return comment
	}
	return " " + comment
}

// foldFlowLines folds text at word boundaries so lines stay within the line width.
func foldFlowLines(textS, indent, mode string, indentAtStart int, hasIndentAtStart bool) string {
	text := u16(textS)
	width, minContent := lineWidth, minContentWidth
	if width < minContent {
		minContent = 0
	}
	endStep := max(1+minContent, 1+width-len(indent))
	if len(text) <= endStep {
		return textS
	}
	var folds []int
	escapedFolds := map[int]bool{}
	end := width - len(indent)
	if hasIndentAtStart {
		if indentAtStart > width-max(2, minContent) {
			folds = append(folds, 0)
		} else {
			end = width - indentAtStart
		}
	}
	at := func(i int) uint16 {
		if i < 0 || i >= len(text) {
			return 0
		}
		return text[i]
	}
	split := -1
	var prev uint16
	i, escStart, escEnd := -1, -1, -1
	for {
		i++
		ch := at(i)
		if ch == 0 {
			break
		}
		if mode == foldQuoted && ch == '\\' {
			escStart = i
			switch at(i + 1) {
			case 'x':
				i += 3
			case 'u':
				i += 5
			case 'U':
				i += 9
			default:
				i++
			}
			escEnd = i
		}
		if ch == '\n' {
			end = i + len(indent) + endStep
			split = -1
		} else {
			if ch == ' ' && prev != 0 && prev != ' ' && prev != '\n' && prev != '\t' {
				if next := at(i + 1); next != 0 && next != ' ' && next != '\n' && next != '\t' {
					split = i
				}
			}
			if i >= end {
				if split > 0 {
					folds = append(folds, split)
					end = split + endStep
					split = -1
				} else if mode == foldQuoted {
					for prev == ' ' || prev == '\t' {
						prev = ch
						i++
						ch = at(i)
					}
					j := escStart - 1
					if i > escEnd+1 {
						j = i - 2
					}
					if escapedFolds[j] {
						return textS
					}
					folds = append(folds, j)
					escapedFolds[j] = true
					end = j + endStep
					split = -1
				}
			}
		}
		prev = ch
	}
	if len(folds) == 0 {
		return textS
	}
	slice := func(a, b int) string {
		a, b = min(max(a, 0), len(text)), min(max(b, 0), len(text))
		if a > b {
			return ""
		}
		return fromU16(text[a:b])
	}
	res := slice(0, folds[0])
	for k, fold := range folds {
		stop := len(text)
		if k+1 < len(folds) && folds[k+1] != 0 {
			stop = folds[k+1]
		}
		if fold == 0 {
			res = "\n" + indent + slice(0, stop)
			continue
		}
		if mode == foldQuoted && escapedFolds[fold] {
			res += fromU16([]uint16{at(fold)}) + "\\"
		}
		res += "\n" + indent + slice(fold+1, stop)
	}
	return res
}

var (
	controlCharsRE   = lazyregexp.New("[\\x00-\\x08\\x0b-\\x1f\\x7f-\\x9f\\x{D800}-\\x{DFFF}]")
	plainSpecialRE   = lazyregexp.New("^[\\n\\t ,[\\]{}#&*!|>'\"%@`]|^[?-]$|^[?-][ \\t]|[\\n:][ \\t]|[ \\t]\\n|[\\n\\t ]#|[\\n\\t :]$")
	flowIndicatorsRE = lazyregexp.New(`[\[\]{},]`)
	wsNewlineRE      = lazyregexp.New("[ \\t]\\n|\\n[ \\t]")
	newlinesRE       = lazyregexp.New(`\n+`)
)

func foldOptsIndentAtStart(c *strCtx) (int, bool) { return c.indentAtStart, c.hasIndentAtStart }

func doubleQuotedString(value string, ctx *strCtx) string {
	json := jsonQuote(value)
	indent := ctx.indent
	if indent == "" && hasDocumentMarker(value) {
		indent = "  "
	}
	var str strings.Builder
	start := 0
	at := func(i int) byte {
		if i >= 0 && i < len(json) {
			return json[i]
		}
		return 0
	}
	jsonLen := u16len(json)
	for i := 0; i < len(json); i++ {
		ch := json[i]
		if ch == ' ' && at(i+1) == '\\' && at(i+2) == 'n' {
			str.WriteString(json[start:i] + "\\ ")
			i++
			start = i
			ch = '\\'
		}
		if ch != '\\' {
			continue
		}
		switch at(i + 1) {
		case 'u':
			str.WriteString(json[start:i])
			code := ""
			if i+2 <= len(json) {
				code = json[i+2 : min(i+6, len(json))]
			}
			switch code {
			case "0000":
				str.WriteString("\\0")
			case "0007":
				str.WriteString("\\a")
			case "000b":
				str.WriteString("\\v")
			case "001b":
				str.WriteString("\\e")
			case "0085":
				str.WriteString("\\N")
			case "00a0":
				str.WriteString("\\_")
			case "2028":
				str.WriteString("\\L")
			case "2029":
				str.WriteString("\\P")
			default:
				if strings.HasPrefix(code, "00") {
					str.WriteString("\\x" + code[2:])
				} else {
					str.WriteString(json[i:min(i+6, len(json))])
				}
			}
			i += 5
			start = i + 1
		case 'n':
			if ctx.implicitKey || at(i+2) == '"' || jsonLen < 40 {
				i++
			} else {
				str.WriteString(json[start:i] + "\n\n")
				for at(i+2) == '\\' && at(i+3) == 'n' && at(i+4) != '"' {
					str.WriteString("\n")
					i += 2
				}
				str.WriteString(indent)
				if at(i+2) == ' ' {
					str.WriteString("\\")
				}
				i++
				start = i + 1
			}
		default:
			i++
		}
	}
	out := json
	if start != 0 {
		out = str.String() + json[min(start, len(json)):]
	}
	if ctx.implicitKey {
		return out
	}
	a, ok := foldOptsIndentAtStart(ctx)
	return foldFlowLines(out, indent, foldQuoted, a, ok)
}

func singleQuotedString(value string, ctx *strCtx) string {
	if (ctx.implicitKey && strings.Contains(value, "\n")) || wsNewlineRE.MatchString(value) {
		return doubleQuotedString(value, ctx)
	}
	indent := ctx.indent
	if indent == "" && hasDocumentMarker(value) {
		indent = "  "
	}
	res := "'" + newlinesRE.ReplaceAllStringFunc(strings.ReplaceAll(value, "'", "''"), func(m string) string { return m + "\n" + indent }) + "'"
	if ctx.implicitKey {
		return res
	}
	a, ok := foldOptsIndentAtStart(ctx)
	return foldFlowLines(res, indent, foldFlow, a, ok)
}

func quotedString(value string, ctx *strCtx) string {
	hasDouble, hasSingle := strings.Contains(value, "\""), strings.Contains(value, "'")
	if hasDouble && !hasSingle {
		return singleQuotedString(value, ctx)
	}
	return doubleQuotedString(value, ctx)
}

func plainString(n *node, value string, ctx *strCtx) string {
	if (ctx.implicitKey && strings.Contains(value, "\n")) || (ctx.inFlow && flowIndicatorsRE.MatchString(value)) {
		return quotedString(value, ctx)
	}
	if plainSpecialRE.MatchString(value) {
		if ctx.implicitKey || ctx.inFlow || !strings.Contains(value, "\n") {
			return quotedString(value, ctx)
		}
		throw("unreachable: block scalar in a flow context")
	}
	if hasDocumentMarker(value) && ctx.indent == "" {
		throw("unreachable: block scalar in a flow context")
	}
	if hasDocumentMarker(value) && ctx.implicitKey && ctx.indent == indentStep {
		return quotedString(value, ctx)
	}
	str := newlinesRE.ReplaceAllStringFunc(value, func(m string) string { return m + "\n" + ctx.indent })
	if ctx.actualString {
		for _, t := range coreScalarTags {
			if t.tag != strTag && t.test.MatchString(str) {
				return quotedString(value, ctx)
			}
		}
	}
	if ctx.implicitKey {
		return str
	}
	a, ok := foldOptsIndentAtStart(ctx)
	return foldFlowLines(str, ctx.indent, foldFlow, a, ok)
}

// stringifyString writes a string scalar; inside a flow context block types are quoted.
func stringifyString(n *node, value string, ctx *strCtx) string {
	typ := n.typ
	if typ != "QUOTE_DOUBLE" && controlCharsRE.MatchString(value) {
		typ = "QUOTE_DOUBLE"
	}
	switch typ {
	case "BLOCK_FOLDED", "BLOCK_LITERAL":
		return quotedString(value, ctx)
	case "QUOTE_DOUBLE":
		return doubleQuotedString(value, ctx)
	case "QUOTE_SINGLE":
		return singleQuotedString(value, ctx)
	}
	return plainString(n, value, ctx)
}

func stringifyNumber(n *node, value float64) string {
	if math.IsNaN(value) {
		return ".nan"
	}
	if math.IsInf(value, 0) {
		if value < 0 {
			return "-.inf"
		}
		return ".inf"
	}
	s := "-0"
	if !(value == 0 && math.Signbit(value)) {
		s = jsnumber.String(value)
	}
	if n.format == "" && n.minFractionDigits != 0 && (n.tag == "" || n.tag == "tag:yaml.org,2002:float") && s != "" && (s[0] >= '0' && s[0] <= '9' || len(s) > 1 && s[0] == '-' && s[1] >= '0' && s[1] <= '9') && !strings.Contains(s, "e") {
		i := strings.IndexByte(s, '.')
		if i < 0 {
			i = len(s)
			s += "."
		}
		d := n.minFractionDigits - (len(s) - i - 1)
		for ; d > 0; d-- {
			s += "0"
		}
	}
	return s
}

func isIntegerValue(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) }

// toExponential is Number.prototype.toExponential() with as many digits as needed.
func toExponential(v float64) string {
	s := strconv.FormatFloat(v, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[0]
	digits := strings.TrimLeft(exp[1:], "0")
	if digits == "" {
		digits = "0"
	}
	return mant + "e" + string(sign) + digits
}

// scalarString is the tag-specific writer of a scalar node: the library's getTagObject followed by tag.stringify.
func scalarString(n *node, ctx *strCtx) string {
	switch v := n.value.(type) {
	case string:
		c := ctx.clone()
		c.actualString = true
		return stringifyString(n, v, c)
	case nil:
		if coreScalarTags[0].test.MatchString(n.source) {
			return n.source
		}
		return "null"
	case bool:
		bt := coreScalarTags[1]
		if n.source != "" && bt.test.MatchString(n.source) && (n.source[0] == 't' || n.source[0] == 'T') == v {
			return n.source
		}
		return strconv.FormatBool(v)
	case float64:
		switch {
		case n.format == "OCT" && isIntegerValue(v) && v >= 0:
			return "0o" + strconv.FormatInt(int64(v), 8)
		case n.format == "HEX" && isIntegerValue(v) && v >= 0:
			return "0x" + strconv.FormatInt(int64(v), 16)
		case n.format == "EXP" && !math.IsNaN(v) && !math.IsInf(v, 0):
			return toExponential(v)
		}
		return stringifyNumber(n, v)
	case Date:
		if math.IsNaN(v.MS) {
			throw("Invalid time value")
		}
		iso := toISOString(v.MS)
		if rest, ok := strings.CutSuffix(iso, ".000Z"); ok {
			return strings.TrimSuffix(rest, "T00:00:00")
		}
		return iso
	case []byte:
		return binaryString(n, v, ctx)
	case Symbol:
		return "<<"
	}
	throw("Tag not resolved for value")
	return ""
}

// toISOString is Date.prototype.toISOString for a finite time value.
func toISOString(ms float64) string {
	t := time.UnixMilli(int64(ms)).UTC()
	year := t.Year()
	var y string
	switch {
	case year >= 0 && year <= 9999:
		y = pad(year, 4)
	case year < 0:
		y = "-" + pad(-year, 6)
	default:
		y = "+" + pad(year, 6)
	}
	return y + t.Format("-01-02T15:04:05.000Z")
}

func pad(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func stringifyComment(c string) string { return commentString(c) }

func addCommentBefore(ctx *strCtx, lines *[]string, comment string) {
	if comment != "" {
		ic := indentComment(stringifyComment(comment), ctx.indent)
		*lines = append(*lines, strings.TrimLeft(ic, jsWhitespace))
	}
}

var anchorInvalidRE = lazyregexp.New("[\\x00-\\x19\\s,\\[\\]{}]")

func anchorIsValid(anchor string) {
	if anchorInvalidRE.MatchString(anchor) {
		throw("Anchor must not contain whitespace or control characters: " + jsonQuote(anchor))
	}
}

// tagString writes a tag the way Directives.tagString does for the default %TAG handles.
func (c *strCtx) tagString(dirs *directives, tag string) string {
	for handle, prefix := range dirs.tags {
		if strings.HasPrefix(tag, prefix) {
			return handle + escapeTagName(tag[len(prefix):])
		}
	}
	if strings.HasPrefix(tag, "!") {
		return tag
	}
	return "!<" + tag + ">"
}

func escapeTagName(tn string) string {
	r := strings.NewReplacer("!", "%21", ",", "%2C", "[", "%5B", "]", "%5D", "{", "%7B", "}", "%7D")
	return r.Replace(tn)
}

// defaultTag reports whether a node's own type needs no tag in the library's output: the tag object chosen for it is a default tag.
func defaultTag(n *node) bool {
	if n.kind == kindScalar {
		switch n.value.(type) {
		case Date, []byte, Symbol:
			return false
		}
		return true
	}
	return n.class == "map" || n.class == "seq"
}

func stringifyItem(ctx *strCtx, doc *document, item any, onComment func()) string {
	if p, ok := item.(*pair); ok {
		return stringifyPair(ctx, doc, p, onComment)
	}
	n := item.(*node)
	if n.kind == kindAlias {
		anchorIsValid(n.source)
		if !ctx.anchors[n.source] {
			throw("Unresolved alias (the anchor must be set before the alias): " + n.source)
		}
		if ctx.implicitKey {
			return "*" + n.source + " "
		}
		return "*" + n.source
	}
	var props []string
	if n.anchor != "" {
		anchorIsValid(n.anchor)
		ctx.anchors[n.anchor] = true
		props = append(props, "&"+n.anchor)
	}
	tag := n.tag
	if tag == "" && !defaultTag(n) {
		tag = knownTagFor(n)
	}
	if tag != "" {
		props = append(props, ctx.tagString(doc.dirs, tag))
	}
	propStr := strings.Join(props, " ")
	if propStr != "" {
		if !ctx.hasIndentAtStart {
			ctx.indentAtStart, ctx.hasIndentAtStart = 0, true
		}
		ctx.indentAtStart += u16len(propStr) + 1
	}
	var str string
	if n.kind == kindScalar {
		str = scalarString(n, ctx)
	} else {
		str = collectionString(ctx, doc, n)
	}
	if propStr == "" {
		return str
	}
	if n.kind == kindScalar || strings.HasPrefix(str, "{") || strings.HasPrefix(str, "[") {
		return propStr + " " + str
	}
	return propStr + "\n" + ctx.indent + str
}

func isCollectionNode(n *node) bool { return n != nil && (n.kind == kindMap || n.kind == kindSeq) }

func collectionString(ctx *strCtx, doc *document, n *node) string {
	flowChars := [2]string{"[", "]"}
	var items []any
	switch {
	case n.kind == kindMap || n.class == "omap" || n.class == "pairs":
		for _, p := range n.pairs {
			items = append(items, p)
		}
		if n.kind == kindMap {
			flowChars = [2]string{"{", "}"}
		}
	default:
		for _, it := range n.items {
			items = append(items, it)
		}
	}
	if n.kind == kindMap {
		if n.class == "set" {
			if !hasAllNullValues(n, true) {
				throw("Set items must all have null values")
			}
			c := ctx.clone()
			c.allNullValues = true
			ctx = c
		} else if !ctx.allNullValues && hasAllNullValues(n, false) {
			c := ctx.clone()
			c.allNullValues = true
			ctx = c
		}
	}
	itemIndent := ctx.indent
	if n.kind != kindMap {
		itemIndent += "  "
	}
	return flowCollectionString(ctx, doc, items, flowChars, itemIndent)
}

// hasAllNullValues is Collection.hasAllNullValues.
func hasAllNullValues(n *node, allowScalar bool) bool {
	if n.kind != kindMap {
		return false
	}
	for _, p := range n.pairs {
		v := p.value
		if v == nil {
			continue
		}
		if allowScalar && v.kind == kindScalar && v.value == nil && v.commentBefore == "" && v.comment == "" && v.tag == "" {
			continue
		}
		return false
	}
	return true
}

func itemNode(item any) *node {
	if n, ok := item.(*node); ok {
		return n
	}
	return nil
}

func flowCollectionString(ctx *strCtx, doc *document, items []any, flowChars [2]string, itemIndent string) string {
	itemIndent += indentStep
	itemCtx := ctx.clone()
	itemCtx.indent = itemIndent
	itemCtx.inFlow = true
	reqNewline := false
	linesAtValue := 0
	var lines []string
	for i, item := range items {
		comment := ""
		if n := itemNode(item); n != nil {
			if n.spaceBefore {
				lines = append(lines, "")
			}
			addCommentBefore(ctx, &lines, n.commentBefore)
			comment = n.comment
		} else if p, ok := item.(*pair); ok {
			ik := p.key
			if ik != nil {
				if ik.spaceBefore {
					lines = append(lines, "")
				}
				addCommentBefore(ctx, &lines, ik.commentBefore)
				if ik.comment != "" {
					reqNewline = true
				}
			}
			if iv := p.value; iv != nil {
				if iv.comment != "" {
					comment = iv.comment
				}
				if iv.commentBefore != "" {
					reqNewline = true
				}
			} else if ik != nil && ik.comment != "" {
				comment = ik.comment
			}
		}
		if comment != "" {
			reqNewline = true
		}
		str := stringifyItem(itemCtx, doc, item, func() { comment = "" })
		reqNewline = reqNewline || len(lines) > linesAtValue || strings.Contains(str, "\n")
		if i < len(items)-1 {
			str += ","
		}
		if comment != "" {
			str += lineComment(str, itemIndent, stringifyComment(comment))
		}
		lines = append(lines, str)
		linesAtValue = len(lines)
	}
	if len(lines) == 0 {
		return flowChars[0] + flowChars[1]
	}
	if !reqNewline {
		total := 2
		for _, l := range lines {
			total += u16len(l) + 2
		}
		reqNewline = total > lineWidth
	}
	if reqNewline {
		var str strings.Builder
		str.WriteString(flowChars[0])
		for _, l := range lines {
			if l != "" {
				str.WriteString("\n" + indentStep + ctx.indent + l)
			} else {
				str.WriteString("\n")
			}
		}
		return str.String() + "\n" + ctx.indent + flowChars[1]
	}
	return flowChars[0] + " " + strings.Join(lines, " ") + " " + flowChars[1]
}

func stringifyPair(ctx *strCtx, doc *document, p *pair, onComment func()) string {
	key, value := p.key, p.value
	keyComment := ""
	if key != nil {
		keyComment = key.comment
	}
	explicitKey := key == nil || key.kind != kindScalar || key.typ == "BLOCK_FOLDED" || key.typ == "BLOCK_LITERAL"
	allNull := ctx.allNullValues
	indent := ctx.indent
	c := ctx.clone()
	c.allNullValues = false
	c.implicitKey = !explicitKey && !allNull
	c.indent = indent + indentStep
	ctx = c
	keyCommentDone := false
	str := stringifyItem(ctx, doc, key, func() { keyCommentDone = true })
	if allNull || value == nil {
		if keyCommentDone && onComment != nil {
			onComment()
		}
		switch {
		case str == "":
			return "?"
		case explicitKey:
			return "? " + str
		}
		return str
	}
	if keyCommentDone {
		keyComment = ""
	}
	if explicitKey {
		if keyComment != "" {
			str += lineComment(str, ctx.indent, stringifyComment(keyComment))
		}
		str = "? " + str + "\n" + indent + ":"
	} else {
		str += ":"
		if keyComment != "" {
			str += lineComment(str, ctx.indent, stringifyComment(keyComment))
		}
	}
	vsb, vcb, valueComment := value.spaceBefore, value.commentBefore, value.comment
	ctx.implicitKey = false
	if !explicitKey && keyComment == "" && value.kind == kindScalar {
		ctx.indentAtStart, ctx.hasIndentAtStart = u16len(str)+1, true
	}
	valueCommentDone := false
	valueStr := stringifyItem(ctx, doc, value, func() { valueCommentDone = true })
	ws := " "
	switch {
	case keyComment != "" || vsb || vcb != "":
		ws = ""
		if vsb {
			ws = "\n"
		}
		if vcb != "" {
			ws += "\n" + indentComment(stringifyComment(vcb), ctx.indent)
		}
		if valueStr == "" {
			if ws == "\n" && valueComment != "" {
				ws = "\n\n"
			}
		} else {
			ws += "\n" + ctx.indent
		}
	case !explicitKey && isCollectionNode(value):
		// ctx.inFlow is set, so a collection value never moves to its own line unless it already spans several.
		if strings.Contains(valueStr, "\n") {
			vs0 := valueStr[0]
			nl0 := strings.IndexByte(valueStr, '\n')
			hasPropsLine := false
			if vs0 == '&' || vs0 == '!' {
				sp0 := strings.IndexByte(valueStr, ' ')
				if vs0 == '&' && sp0 != -1 && sp0 < nl0 && sp0+1 < len(valueStr) && valueStr[sp0+1] == '!' {
					if k := strings.IndexByte(valueStr[sp0+1:], ' '); k >= 0 {
						sp0 = sp0 + 1 + k
					} else {
						sp0 = -1
					}
				}
				if sp0 == -1 || nl0 < sp0 {
					hasPropsLine = true
				}
			}
			if !hasPropsLine {
				ws = "\n" + ctx.indent
			}
		}
	case valueStr == "" || valueStr[0] == '\n':
		ws = ""
	}
	str += ws + valueStr
	if valueCommentDone && onComment != nil {
		onComment()
	}
	return str
}
