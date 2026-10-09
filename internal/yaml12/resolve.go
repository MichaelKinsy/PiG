package yaml12

// Ports yaml 2.9.0 src/compose/resolve-props.ts, resolve-end.ts, resolve-flow-scalar.ts, resolve-block-scalar.ts and the util-*.ts helpers. Copyright Eemeli Aro, ISC licence (see lexer.go). Comment text and node spacing, which never reach a parsed value or an error, are not kept.

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type propsResult struct {
	comma, found, anchor, tag, newlineAfterProp *Token
	hasNewline, hasComment, spaceBefore         bool
	comment                                     string
	end, start                                  int
}

type propsOptions struct {
	flow         string
	indicator    string
	next         *Token
	offset       int
	parentIndent int
	startOnNL    bool
}

func resolveProps(tokens []*Token, o propsOptions, onError errorFn) propsResult {
	var res propsResult
	atNewline, hasSpace := o.startOnNL, o.startOnNL
	reqSpace := false
	commentSep := ""
	var tab *Token
	startSet := false
	start := 0
	for _, token := range tokens {
		if reqSpace {
			if token.Type != "space" && token.Type != "newline" && token.Type != "comma" {
				onError(posOfOffset(token.Offset), "MISSING_CHAR", "Tags and anchors must be separated from the next token by white space")
			}
			reqSpace = false
		}
		if tab != nil {
			if atNewline && token.Type != "comment" && token.Type != "newline" {
				onError(posOfToken(tab), "TAB_AS_INDENT", "Tabs are not allowed as indentation")
			}
			tab = nil
		}
		switch {
		case token.Type == "space":
			if o.flow == "" && (o.indicator != "doc-start" || o.next == nil || o.next.Type != "flow-collection") && strings.Contains(token.Source, "\t") {
				tab = token
			}
			hasSpace = true
		case token.Type == "comment":
			if !hasSpace {
				onError(posOfToken(token), "MISSING_CHAR", "Comments must be separated from other tokens by white space characters")
			}
			res.hasComment = true
			cb := token.Source[1:]
			if cb == "" {
				cb = " "
			}
			if res.comment == "" {
				res.comment = cb
			} else {
				res.comment += commentSep + cb
			}
			commentSep = ""
			atNewline = false
		case token.Type == "newline":
			if atNewline {
				if res.comment != "" {
					res.comment += token.Source
				} else if res.found == nil || o.indicator != "seq-item-ind" {
					res.spaceBefore = true
				}
			} else {
				commentSep += token.Source
			}
			atNewline = true
			res.hasNewline = true
			if res.anchor != nil || res.tag != nil {
				res.newlineAfterProp = token
			}
			hasSpace = true
		case token.Type == "anchor":
			if res.anchor != nil {
				onError(posOfToken(token), "MULTIPLE_ANCHORS", "A node can have at most one anchor")
			}
			if strings.HasSuffix(token.Source, ":") {
				onError(posOfOffset(token.Offset+len(token.Source)-1), "BAD_ALIAS", "Anchor ending in : is ambiguous", true)
			}
			res.anchor = token
			if !startSet {
				start, startSet = token.Offset, true
			}
			atNewline, hasSpace, reqSpace = false, false, true
		case token.Type == "tag":
			if res.tag != nil {
				onError(posOfToken(token), "MULTIPLE_TAGS", "A node can have at most one tag")
			}
			res.tag = token
			if !startSet {
				start, startSet = token.Offset, true
			}
			atNewline, hasSpace, reqSpace = false, false, true
		case token.Type == o.indicator:
			if res.anchor != nil || res.tag != nil {
				onError(posOfToken(token), "BAD_PROP_ORDER", "Anchors and tags must be after the "+token.Source+" indicator")
			}
			if res.found != nil {
				where := o.flow
				if where == "" {
					where = "collection"
				}
				onError(posOfToken(token), "UNEXPECTED_TOKEN", "Unexpected "+token.Source+" in "+where)
			}
			res.found = token
			atNewline = o.indicator == "seq-item-ind" || o.indicator == "explicit-key-ind"
			hasSpace = false
		case token.Type == "comma" && o.flow != "":
			if res.comma != nil {
				onError(posOfToken(token), "UNEXPECTED_TOKEN", "Unexpected , in "+o.flow)
			}
			res.comma = token
			atNewline, hasSpace = false, false
		default:
			onError(posOfToken(token), "UNEXPECTED_TOKEN", "Unexpected "+token.Type+" token")
			atNewline, hasSpace = false, false
		}
	}
	end := o.offset
	if len(tokens) > 0 {
		last := tokens[len(tokens)-1]
		end = last.Offset + len(last.Source)
	}
	if reqSpace && o.next != nil && o.next.Type != "space" && o.next.Type != "newline" && o.next.Type != "comma" && (o.next.Type != "scalar" || o.next.Source != "") {
		onError(posOfOffset(o.next.Offset), "MISSING_CHAR", "Tags and anchors must be separated from the next token by white space")
	}
	if tab != nil && ((atNewline && tab.Indent <= o.parentIndent) || (o.next != nil && (o.next.Type == "block-map" || o.next.Type == "block-seq"))) {
		onError(posOfToken(tab), "TAB_AS_INDENT", "Tabs are not allowed as indentation")
	}
	res.end = end
	if startSet {
		res.start = start
	} else {
		res.start = end
	}
	return res
}

// resolveEnd reports the offset after a node's trailing tokens and the comment they carry.
func resolveEnd(end []*Token, offset int, reqSpace bool, onError errorFn) (int, string) {
	comment, sep := "", ""
	hasSpace := false
	for _, token := range end {
		switch token.Type {
		case "space":
			hasSpace = true
		case "comment":
			if reqSpace && !hasSpace {
				onError(posOfToken(token), "MISSING_CHAR", "Comments must be separated from other tokens by white space characters")
			}
			cb := token.Source[1:]
			if cb == "" {
				cb = " "
			}
			if comment == "" {
				comment = cb
			} else {
				comment += sep + cb
			}
			sep = ""
		case "newline":
			if comment != "" {
				sep += token.Source
			}
			hasSpace = true
		default:
			onError(posOfToken(token), "UNEXPECTED_TOKEN", "Unexpected "+token.Type+" at node end")
		}
		offset += len(token.Source)
	}
	return offset, comment
}

func containsNewline(key *Token) bool {
	if key == nil {
		return false
	}
	switch key.Type {
	case "alias", "scalar", "double-quoted-scalar", "single-quoted-scalar":
		if strings.Contains(key.Source, "\n") {
			return true
		}
		for _, st := range key.End {
			if st.Type == "newline" {
				return true
			}
		}
		return false
	case "flow-collection":
		for _, it := range key.Items {
			for _, st := range it.Start {
				if st.Type == "newline" {
					return true
				}
			}
			for _, st := range it.Sep {
				if st.Type == "newline" {
					return true
				}
			}
			if containsNewline(it.Key) || containsNewline(it.Value) {
				return true
			}
		}
		return false
	}
	return true
}

func emptyScalarPosition(offset int, before []*Token) int {
	if before != nil {
		for i := len(before) - 1; i >= 0; i-- {
			st := before[i]
			switch st.Type {
			case "space", "comment", "newline":
				offset -= len(st.Source)
				continue
			}
			i++
			for i < len(before) && before[i].Type == "space" {
				offset += len(before[i].Source)
				i++
			}
			break
		}
	}
	return offset
}

type scalarResult struct {
	value   string
	rng     [3]int
	comment string
	typ     string
}

func resolveFlowScalar(scalar *Token, strict bool, onError errorFn) scalarResult {
	offset, source := scalar.Offset, scalar.Source
	rel := func(r int, code, msg string) { onError(posOfOffset(offset+r), code, msg) }
	var value, typ string
	switch scalar.Type {
	case "scalar":
		value, typ = plainValue(source, rel), "PLAIN"
	case "single-quoted-scalar":
		value, typ = singleQuotedValue(source, rel), "QUOTE_SINGLE"
	case "double-quoted-scalar":
		value, typ = doubleQuotedValue(source, rel), "QUOTE_DOUBLE"
	default:
		onError(posOfToken(scalar), "UNEXPECTED_TOKEN", "Expected a flow scalar value, but found: "+scalar.Type)
		return scalarResult{rng: [3]int{offset, offset + len(source), offset + len(source)}}
	}
	valueEnd := offset + len(source)
	end, comment := resolveEnd(scalar.End, valueEnd, strict, onError)
	return scalarResult{value, [3]int{offset, valueEnd, end}, comment, typ}
}

func plainValue(source string, onError func(int, string, string)) string {
	bad := ""
	if source != "" {
		switch source[0] {
		case '\t':
			bad = "a tab character"
		case ',':
			bad = "flow indicator character ,"
		case '%':
			bad = "directive indicator character %"
		case '|', '>':
			bad = "block scalar indicator " + source[:1]
		case '@', '`':
			bad = "reserved character " + source[:1]
		}
	}
	if bad != "" {
		onError(0, "BAD_SCALAR_START", "Plain value cannot start with "+bad)
	}
	return foldLines(source)
}

func singleQuotedValue(source string, onError func(int, string, string)) string {
	if source == "" || source[len(source)-1] != '\'' || len(source) == 1 {
		onError(len(source), "MISSING_CHAR", "Missing closing 'quote")
	}
	inner := ""
	if len(source) >= 2 {
		inner = source[1 : len(source)-1]
	}
	return strings.ReplaceAll(foldLines(inner), "''", "'")
}

func trimBlanksLeft(s string) string  { return strings.TrimLeft(s, " \t") }
func trimBlanksRight(s string) string { return strings.TrimRight(s, " \t") }

// foldLines folds the line breaks of a flow scalar the way the library's sticky regular expressions do.
func foldLines(source string) string {
	n := strings.IndexByte(source, '\n')
	if n < 0 {
		return source
	}
	seg := strings.TrimSuffix(source[:n], "\r")
	var res strings.Builder
	res.WriteString(trimBlanksRight(seg))
	sep := " "
	pos := n + 1
	for {
		i := strings.IndexByte(source[pos:], '\n')
		if i < 0 {
			break
		}
		line := trimBlanksLeft(trimBlanksRight(strings.TrimSuffix(source[pos:pos+i], "\r")))
		if line == "" {
			if sep == "\n" {
				res.WriteString(sep)
			} else {
				sep = "\n"
			}
		} else {
			res.WriteString(sep + line)
			sep = " "
		}
		pos += i + 1
	}
	return res.String() + sep + trimBlanksLeft(source[pos:])
}

var escapeCodes = map[byte]string{
	'0': "\x00", 'a': "\x07", 'b': "\b", 'e': "\x1b", 'f': "\f", 'n': "\n", 'r': "\r", 't': "\t", 'v': "\v",
	'N': "\u0085", '_': "\u00a0", 'L': "\u2028", 'P': "\u2029", ' ': " ", '"': "\"", '/': "/", '\\': "\\", '\t': "\t",
}

func charAt(s string, i int) int {
	if i < 0 || i >= len(s) {
		return eof
	}
	return int(s[i])
}

// substr is String.prototype.substr over bytes, clamped to the string.
func substr(s string, from, length int) string {
	if from < 0 {
		from = max(len(s)+from, 0)
	}
	if from >= len(s) {
		return ""
	}
	return s[from:min(from+length, len(s))]
}

// substr2 is substr(i-1, 2) taken on whole characters.
func substrChars(s string, from int) string {
	if from < 0 || from >= len(s) {
		return ""
	}
	end := from + 1
	if end < len(s) {
		_, size := utf8.DecodeRuneInString(s[end:])
		end += size
	}
	return s[from:end]
}

func doubleQuotedValue(source string, onError func(int, string, string)) string {
	var res strings.Builder
	for i := 1; i < len(source)-1; i++ {
		ch := source[i]
		switch {
		case ch == '\r' && charAt(source, i+1) == '\n':
			continue
		case ch == '\n':
			fold, offset := foldNewline(source, i)
			res.WriteString(fold)
			i = offset
		case ch == '\\':
			i++
			next := charAt(source, i)
			cc, known := escapeCodes[byte(next)]
			switch {
			case known && next != eof:
				res.WriteString(cc)
			case next == '\n':
				next = charAt(source, i+1)
				for next == ' ' || next == '\t' {
					i++
					next = charAt(source, i+1)
				}
			case next == '\r' && charAt(source, i+1) == '\n':
				i++
				next = charAt(source, i+1)
				for next == ' ' || next == '\t' {
					i++
					next = charAt(source, i+1)
				}
			case next == 'x' || next == 'u' || next == 'U':
				length := 2
				switch next {
				case 'u':
					length = 4
				case 'U':
					length = 8
				}
				res.WriteString(parseCharCode(source, i+1, length, onError))
				i += length
			default:
				raw := substrChars(source, i-1)
				onError(i-1, "BAD_DQ_ESCAPE", "Invalid escape sequence "+raw)
				res.WriteString(raw)
			}
		case ch == ' ' || ch == '\t':
			wsStart := i
			next := charAt(source, i+1)
			for next == ' ' || next == '\t' {
				i++
				next = charAt(source, i+1)
			}
			if next != '\n' && (next != '\r' || charAt(source, i+2) != '\n') {
				if i > wsStart {
					res.WriteString(source[wsStart : i+1])
				} else {
					res.WriteByte(ch)
				}
			}
		default:
			res.WriteByte(ch)
		}
	}
	if source == "" || source[len(source)-1] != '"' || len(source) == 1 {
		onError(len(source), "MISSING_CHAR", "Missing closing \"quote")
	}
	return res.String()
}

func foldNewline(source string, offset int) (string, int) {
	fold := ""
	ch := charAt(source, offset+1)
	for ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
		if ch == '\r' && charAt(source, offset+2) != '\n' {
			break
		}
		if ch == '\n' {
			fold += "\n"
		}
		offset++
		ch = charAt(source, offset+1)
	}
	if fold == "" {
		fold = " "
	}
	return fold, offset
}

func parseCharCode(source string, offset, length int, onError func(int, string, string)) string {
	cc := substr(source, offset, length)
	ok := len(cc) == length
	if ok {
		for i := 0; i < len(cc); i++ {
			if !isHex(int(cc[i])) {
				ok = false
			}
		}
	}
	if ok {
		code, err := strconv.ParseUint(cc, 16, 64)
		if err == nil && code <= 0x10FFFF {
			if code >= 0xD800 && code <= 0xDFFF {
				return "\ufffd"
			}
			return string(rune(code))
		}
	}
	raw := substr(source, offset-2, length+2)
	onError(offset-2, "BAD_DQ_ESCAPE", "Invalid escape sequence "+raw)
	return raw
}

type blockHeader struct {
	mode, chomp string
	comment     string
	indent      int
	length      int
}

func resolveBlockScalar(atRoot, strict bool, scalar *Token, onError errorFn) scalarResult {
	start := scalar.Offset
	header, ok := parseBlockScalarHeader(scalar, strict, onError)
	if !ok {
		return scalarResult{rng: [3]int{start, start, start}}
	}
	literal := header.mode != ">"
	typ := "BLOCK_FOLDED"
	if literal {
		typ = "BLOCK_LITERAL"
	}
	var lines [][2]string
	if scalar.Source != "" {
		lines = splitLines(scalar.Source)
	}
	chompStart := len(lines)
	for i, line := range slices.Backward(lines) {
		content := line[1]
		if content == "" || content == "\r" {
			chompStart = i
		} else {
			break
		}
	}
	if chompStart == 0 {
		value := ""
		if header.chomp == "+" && len(lines) > 0 {
			value = strings.Repeat("\n", max(1, len(lines)-1))
		}
		end := start + header.length
		if scalar.Source != "" {
			end += len(scalar.Source)
		}
		return scalarResult{value, [3]int{start, end, end}, header.comment, typ}
	}
	trimIndent := scalar.Indent + header.indent
	offset := scalar.Offset + header.length
	contentStart := 0
	for i := 0; i < chompStart; i++ {
		indent, content := lines[i][0], lines[i][1]
		if content == "" || content == "\r" {
			if header.indent == 0 && len(indent) > trimIndent {
				trimIndent = len(indent)
			}
		} else {
			if len(indent) < trimIndent {
				onError(posOfOffset(offset+len(indent)), "MISSING_CHAR", "Block scalars with more-indented leading empty lines must use an explicit indentation indicator")
			}
			if header.indent == 0 {
				trimIndent = len(indent)
			}
			contentStart = i
			if trimIndent == 0 && !atRoot {
				onError(posOfOffset(offset), "BAD_INDENT", "Block scalar values in collections must be indented")
			}
			break
		}
		offset += len(indent) + len(content) + 1
	}
	for i := len(lines) - 1; i >= chompStart; i-- {
		if len(lines[i][0]) > trimIndent {
			chompStart = i + 1
		}
	}
	sliceIndent := func(s string) string {
		if trimIndent >= len(s) {
			return ""
		}
		return s[trimIndent:]
	}
	var value strings.Builder
	sep := ""
	prevMoreIndented := false
	for i := 0; i < contentStart; i++ {
		value.WriteString(sliceIndent(lines[i][0]) + "\n")
	}
	for i := contentStart; i < chompStart; i++ {
		indent, content := lines[i][0], lines[i][1]
		offset += len(indent) + len(content) + 1
		crlf := strings.HasSuffix(content, "\r")
		if crlf {
			content = content[:len(content)-1]
		}
		if content != "" && len(indent) < trimIndent {
			src := "first line"
			if header.indent != 0 {
				src = "explicit indentation indicator"
			}
			delta := 1
			if crlf {
				delta = 2
			}
			onError(posOfOffset(offset-len(content)-delta), "BAD_INDENT", "Block scalar lines must not be less indented than their "+src)
			indent = ""
		}
		switch {
		case literal:
			value.WriteString(sep + sliceIndent(indent) + content)
			sep = "\n"
		case len(indent) > trimIndent || (content != "" && content[0] == '\t'):
			if sep == " " {
				sep = "\n"
			} else if !prevMoreIndented && sep == "\n" {
				sep = "\n\n"
			}
			value.WriteString(sep + sliceIndent(indent) + content)
			sep = "\n"
			prevMoreIndented = true
		case content == "":
			if sep == "\n" {
				value.WriteString("\n")
			} else {
				sep = "\n"
			}
		default:
			value.WriteString(sep + content)
			sep = " "
			prevMoreIndented = false
		}
	}
	switch header.chomp {
	case "-":
	case "+":
		for i := chompStart; i < len(lines); i++ {
			value.WriteString("\n" + sliceIndent(lines[i][0]))
		}
		if !strings.HasSuffix(value.String(), "\n") {
			value.WriteString("\n")
		}
	default:
		value.WriteString("\n")
	}
	end := start + header.length + len(scalar.Source)
	return scalarResult{value.String(), [3]int{start, end, end}, header.comment, typ}
}

func parseBlockScalarHeader(scalar *Token, strict bool, onError errorFn) (blockHeader, bool) {
	if len(scalar.Props) == 0 || scalar.Props[0].Type != "block-scalar-header" {
		var at pos
		if len(scalar.Props) > 0 {
			at = posOfToken(scalar.Props[0])
		}
		onError(at, "IMPOSSIBLE", "Block scalar header not found")
		return blockHeader{}, false
	}
	source := scalar.Props[0].Source
	h := blockHeader{mode: source[:1]}
	errAt := -1
	for i := 1; i < len(source); i++ {
		ch := source[i]
		switch {
		case h.chomp == "" && (ch == '-' || ch == '+'):
			h.chomp = string(ch)
		case ch >= '1' && ch <= '9' && h.indent == 0:
			h.indent = int(ch - '0')
		case errAt == -1:
			errAt = scalar.Offset + i
		}
	}
	if errAt != -1 {
		onError(posOfOffset(errAt), "UNEXPECTED_TOKEN", "Block scalar header includes extra characters: "+source)
	}
	hasSpace := false
	h.length = len(source)
	for _, token := range scalar.Props[1:] {
		switch token.Type {
		case "space":
			hasSpace = true
			h.length += len(token.Source)
		case "newline":
			h.length += len(token.Source)
		case "comment":
			if strict && !hasSpace {
				onError(posOfToken(token), "MISSING_CHAR", "Comments must be separated from other tokens by white space characters")
			}
			h.length += len(token.Source)
			h.comment = token.Source[1:]
		case "error":
			onError(posOfToken(token), "UNEXPECTED_TOKEN", token.Message)
			h.length += len(token.Source)
		default:
			onError(posOfToken(token), "UNEXPECTED_TOKEN", "Unexpected token in block scalar header: "+token.Type)
			h.length += len(token.Source)
		}
	}
	return h, true
}

// splitLines is the library's split(/\n( *)/) regrouped into [indent, content] pairs.
func splitLines(source string) [][2]string {
	var parts []string
	rest := source
	for {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			parts = append(parts, rest)
			break
		}
		parts = append(parts, rest[:i])
		j := i + 1
		for j < len(rest) && rest[j] == ' ' {
			j++
		}
		parts = append(parts, rest[i+1:j])
		rest = rest[j:]
	}
	first := parts[0]
	k := 0
	for k < len(first) && first[k] == ' ' {
		k++
	}
	lines := [][2]string{{first[:k], first[k:]}}
	for i := 1; i < len(parts); i += 2 {
		content := ""
		if i+1 < len(parts) {
			content = parts[i+1]
		}
		lines = append(lines, [2]string{parts[i], content})
	}
	return lines
}
