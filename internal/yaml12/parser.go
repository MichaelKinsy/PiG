package yaml12

import "slices"

// Ports yaml 2.9.0 src/parse/parser.ts and the token helpers of src/parse/cst.ts. Copyright Eemeli Aro, ISC licence (see lexer.go).

// Token is a CST token. Field use follows the token type exactly as in the library: End and Sep are nil where the library leaves the property undefined.
type Token struct {
	Type    string
	Offset  int
	Indent  int
	Source  string
	Message string
	// Start is the props of a document.
	Start []*Token
	// StartTok is the opening bracket of a flow collection.
	StartTok *Token
	End      []*Token
	Props    []*Token
	Items    []*Item
	Value    *Token
}

// Item is an entry of a block map, block sequence or flow collection.
type Item struct {
	Start       []*Token
	Key         *Token
	Sep         []*Token
	Value       *Token
	ExplicitKey bool
}

func tokenType(source string) string {
	switch source {
	case bom:
		return "byte-order-mark"
	case tokDocument:
		return "doc-mode"
	case tokFlowEnd:
		return "flow-error-end"
	case tokScalar:
		return "scalar"
	case "---":
		return "doc-start"
	case "...":
		return "doc-end"
	case "", "\n", "\r\n":
		return "newline"
	case "-":
		return "seq-item-ind"
	case "?":
		return "explicit-key-ind"
	case ":":
		return "map-value-ind"
	case "{":
		return "flow-map-start"
	case "}":
		return "flow-map-end"
	case "[":
		return "flow-seq-start"
	case "]":
		return "flow-seq-end"
	case ",":
		return "comma"
	}
	switch source[0] {
	case ' ', '\t':
		return "space"
	case '#':
		return "comment"
	case '%':
		return "directive-line"
	case '*':
		return "alias"
	case '&':
		return "anchor"
	case '!':
		return "tag"
	case '\'':
		return "single-quoted-scalar"
	case '"':
		return "double-quoted-scalar"
	case '|', '>':
		return "block-scalar-header"
	}
	return ""
}

func includesToken(list []*Token, typ string) bool {
	for _, t := range list {
		if t.Type == typ {
			return true
		}
	}
	return false
}

func findNonEmptyIndex(list []*Token) int {
	for i, t := range list {
		switch t.Type {
		case "space", "comment", "newline":
		default:
			return i
		}
	}
	return -1
}

func isFlowToken(t *Token) bool {
	if t == nil {
		return false
	}
	switch t.Type {
	case "alias", "scalar", "single-quoted-scalar", "double-quoted-scalar", "flow-collection":
		return true
	}
	return false
}

// getPrevProps returns the props array that precedes a new collection's first key; the library returns an array it then splices in place, so this returns its address.
func getPrevProps(parent *Token) *[]*Token {
	switch parent.Type {
	case "document":
		return &parent.Start
	case "block-map":
		it := parent.Items[len(parent.Items)-1]
		if it.Sep != nil {
			return &it.Sep
		}
		return &it.Start
	case "block-seq":
		return &parent.Items[len(parent.Items)-1].Start
	}
	return &[]*Token{}
}

// getFirstKeyStartProps splices the props of the first key off the end of *prev and returns them.
func getFirstKeyStartProps(prev *[]*Token) []*Token {
	if len(*prev) == 0 {
		return []*Token{}
	}
	p := *prev
	i := len(p)
	for {
		i--
		if i < 0 {
			break
		}
		switch p[i].Type {
		case "doc-start", "explicit-key-ind", "map-value-ind", "seq-item-ind", "newline":
			goto found
		}
	}
found:
	for {
		i++
		if i < len(p) && p[i].Type == "space" {
			continue
		}
		break
	}
	tail := append([]*Token{}, p[i:]...)
	*prev = p[:i]
	return tail
}

func fixFlowSeqItems(fc *Token) {
	if fc.StartTok.Type != "flow-seq-start" {
		return
	}
	for _, it := range fc.Items {
		if it.Sep != nil && it.Value == nil && !includesToken(it.Start, "explicit-key-ind") && !includesToken(it.Sep, "map-value-ind") {
			if it.Key != nil {
				it.Value = it.Key
			}
			it.Key = nil
			if isFlowToken(it.Value) {
				if it.Value.End != nil {
					it.Value.End = append(it.Value.End, it.Sep...)
				} else {
					it.Value.End = it.Sep
				}
			} else {
				it.Start = append(it.Start, it.Sep...)
			}
			it.Sep = nil
		}
	}
}

type parser struct {
	atNewLine bool
	atScalar  bool
	indent    int
	offset    int
	onKeyLine bool
	stack     []*Token
	source    string
	typ       string
	out       []*Token
	// lineStarts holds the byte offset where each source line starts, as the library LineCounter collects them through onNewLine.
	lineStarts []int
}

// parseCST returns the CST tokens of source: one per document, directive, comment, space, newline or error.
func parseCST(source string) []*Token {
	tokens, _ := parseCSTWithLines(source)
	return tokens
}

// parseCSTWithLines also returns the line start offsets.
func parseCSTWithLines(source string) ([]*Token, []int) {
	p := &parser{atNewLine: true}
	p.lineStarts = append(p.lineStarts, 0)
	for _, lexeme := range lex(source) {
		p.next(lexeme)
	}
	for len(p.stack) > 0 {
		p.pop(nil)
	}
	return p.out, p.lineStarts
}

// newLinesIn records the line starts inside a multi-line token that begins at the current offset.
func (p *parser) newLinesIn(source string) {
	for i := 0; i < len(source); i++ {
		if source[i] == '\n' {
			p.lineStarts = append(p.lineStarts, p.offset+i+1)
		}
	}
}

func (p *parser) emit(t *Token) { p.out = append(p.out, t) }

func (p *parser) next(source string) {
	p.source = source
	if p.atScalar {
		p.atScalar = false
		p.step()
		p.offset += len(source)
		return
	}
	typ := tokenType(source)
	switch typ {
	case "":
		message := "Not a YAML token: " + source
		p.pop(&Token{Type: "error", Offset: p.offset, Message: message, Source: source})
		p.offset += len(source)
	case "scalar":
		p.atNewLine = false
		p.atScalar = true
		p.typ = "scalar"
	default:
		p.typ = typ
		p.step()
		switch typ {
		case "newline":
			p.atNewLine = true
			p.indent = 0
			p.lineStarts = append(p.lineStarts, p.offset+len(source))
		case "space":
			if p.atNewLine && source[0] == ' ' {
				p.indent += len(source)
			}
		case "explicit-key-ind", "map-value-ind", "seq-item-ind":
			if p.atNewLine {
				p.indent += len(source)
			}
		case "doc-mode", "flow-error-end":
			return
		default:
			p.atNewLine = false
		}
		p.offset += len(source)
	}
}

func (p *parser) sourceToken() *Token {
	return &Token{Type: p.typ, Offset: p.offset, Indent: p.indent, Source: p.source}
}

func (p *parser) peek(n int) *Token {
	i := len(p.stack) - n
	if i < 0 || i >= len(p.stack) {
		return nil
	}
	return p.stack[i]
}

func (p *parser) step() {
	top := p.peek(1)
	if p.typ == "doc-end" && (top == nil || top.Type != "doc-end") {
		for len(p.stack) > 0 {
			p.pop(nil)
		}
		p.stack = append(p.stack, &Token{Type: "doc-end", Offset: p.offset, Source: p.source})
		return
	}
	if top == nil {
		p.stream()
		return
	}
	switch top.Type {
	case "document":
		p.document(top)
	case "alias", "scalar", "single-quoted-scalar", "double-quoted-scalar":
		p.scalar(top)
	case "block-scalar":
		p.blockScalar(top)
	case "block-map":
		p.blockMap(top)
	case "block-seq":
		p.blockSequence(top)
	case "flow-collection":
		p.flowCollection(top)
	case "doc-end":
		p.documentEnd(top)
	default:
		p.pop(nil)
	}
}

func lastItem(items []*Item) *Item {
	if len(items) == 0 {
		return nil
	}
	return items[len(items)-1]
}

func (p *parser) pop(errTok *Token) {
	token := errTok
	if token == nil && len(p.stack) > 0 {
		token = p.stack[len(p.stack)-1]
		p.stack = p.stack[:len(p.stack)-1]
	}
	if token == nil {
		p.emit(&Token{Type: "error", Offset: p.offset, Source: "", Message: "Tried to pop an empty stack"})
		return
	}
	if len(p.stack) == 0 {
		p.emit(token)
		return
	}
	top := p.peek(1)
	if token.Type == "block-scalar" {
		token.Indent = top.Indent
	} else if token.Type == "flow-collection" && top.Type == "document" {
		token.Indent = 0
	}
	if token.Type == "flow-collection" {
		fixFlowSeqItems(token)
	}
	switch top.Type {
	case "document":
		top.Value = token
	case "block-scalar":
		top.Props = append(top.Props, token)
	case "block-map":
		it := lastItem(top.Items)
		switch {
		case it.Value != nil:
			top.Items = append(top.Items, &Item{Start: []*Token{}, Key: token, Sep: []*Token{}})
			p.onKeyLine = true
			return
		case it.Sep != nil:
			it.Value = token
		default:
			it.Key, it.Sep = token, []*Token{}
			p.onKeyLine = !it.ExplicitKey
			return
		}
	case "block-seq":
		it := lastItem(top.Items)
		if it.Value != nil {
			top.Items = append(top.Items, &Item{Start: []*Token{}, Value: token})
		} else {
			it.Value = token
		}
	case "flow-collection":
		it := lastItem(top.Items)
		switch {
		case it == nil || it.Value != nil:
			top.Items = append(top.Items, &Item{Start: []*Token{}, Key: token, Sep: []*Token{}})
		case it.Sep != nil:
			it.Value = token
		default:
			it.Key, it.Sep = token, []*Token{}
		}
		return
	default:
		p.pop(nil)
		p.pop(token)
	}
	if (top.Type == "document" || top.Type == "block-map" || top.Type == "block-seq") && (token.Type == "block-map" || token.Type == "block-seq") {
		last := lastItem(token.Items)
		if last != nil && last.Sep == nil && last.Value == nil && len(last.Start) > 0 && findNonEmptyIndex(last.Start) == -1 {
			ok := token.Indent == 0
			if !ok {
				ok = true
				for _, st := range last.Start {
					if st.Type == "comment" && st.Indent >= token.Indent {
						ok = false
						break
					}
				}
			}
			if ok {
				if top.Type == "document" {
					top.End = last.Start
				} else {
					top.Items = append(top.Items, &Item{Start: last.Start})
				}
				token.Items = token.Items[:len(token.Items)-1]
			}
		}
	}
}

func (p *parser) stream() {
	switch p.typ {
	case "directive-line":
		p.emit(&Token{Type: "directive", Offset: p.offset, Source: p.source})
		return
	case "byte-order-mark", "space", "comment", "newline":
		p.emit(p.sourceToken())
		return
	case "doc-mode", "doc-start":
		doc := &Token{Type: "document", Offset: p.offset, Start: []*Token{}}
		if p.typ == "doc-start" {
			doc.Start = append(doc.Start, p.sourceToken())
		}
		p.stack = append(p.stack, doc)
		return
	}
	p.emit(&Token{Type: "error", Offset: p.offset, Message: "Unexpected " + p.typ + " token in YAML stream", Source: p.source})
}

func (p *parser) document(doc *Token) {
	if doc.Value != nil {
		p.lineEnd(doc)
		return
	}
	switch p.typ {
	case "doc-start":
		if findNonEmptyIndex(doc.Start) != -1 {
			p.pop(nil)
			p.step()
		} else {
			doc.Start = append(doc.Start, p.sourceToken())
		}
		return
	case "anchor", "tag", "space", "comment", "newline":
		doc.Start = append(doc.Start, p.sourceToken())
		return
	}
	if bv := p.startBlockValue(doc); bv != nil {
		p.stack = append(p.stack, bv)
	} else {
		p.emit(&Token{Type: "error", Offset: p.offset, Message: "Unexpected " + p.typ + " token in YAML document", Source: p.source})
	}
}

func (p *parser) scalar(scalar *Token) {
	if p.typ != "map-value-ind" {
		p.lineEnd(scalar)
		return
	}
	prev := getPrevProps(p.peek(2))
	start := getFirstKeyStartProps(prev)
	var sep []*Token
	if scalar.End != nil {
		sep = append(slices.Clone(scalar.End), p.sourceToken())
		scalar.End = nil
	} else {
		sep = []*Token{p.sourceToken()}
	}
	m := &Token{Type: "block-map", Offset: scalar.Offset, Indent: scalar.Indent, Items: []*Item{{Start: start, Key: scalar, Sep: sep}}}
	p.onKeyLine = true
	p.stack[len(p.stack)-1] = m
}

func (p *parser) blockScalar(scalar *Token) {
	switch p.typ {
	case "space", "comment", "newline":
		scalar.Props = append(scalar.Props, p.sourceToken())
	case "scalar":
		scalar.Source = p.source
		p.atNewLine = true
		p.indent = 0
		p.newLinesIn(p.source)
		p.pop(nil)
	default:
		p.pop(nil)
		p.step()
	}
}

func (p *parser) newBlockMap(items ...*Item) *Token {
	return &Token{Type: "block-map", Offset: p.offset, Indent: p.indent, Items: items}
}

// endOf reports the end array of a token, or nil where the token has none.
func endOf(t *Token) []*Token { return t.End }

func (p *parser) blockMap(m *Token) {
	it := lastItem(m.Items)
	switch p.typ {
	case "newline":
		p.onKeyLine = false
		switch {
		case it.Value != nil:
			end := endOf(it.Value)
			if len(end) > 0 && end[len(end)-1].Type == "comment" {
				it.Value.End = append(it.Value.End, p.sourceToken())
			} else {
				m.Items = append(m.Items, &Item{Start: []*Token{p.sourceToken()}})
			}
		case it.Sep != nil:
			it.Sep = append(it.Sep, p.sourceToken())
		default:
			it.Start = append(it.Start, p.sourceToken())
		}
		return
	case "space", "comment":
		switch {
		case it.Value != nil:
			m.Items = append(m.Items, &Item{Start: []*Token{p.sourceToken()}})
		case it.Sep != nil:
			it.Sep = append(it.Sep, p.sourceToken())
		default:
			if p.atIndentedComment(it.Start, m.Indent) {
				var prev *Item
				if len(m.Items) >= 2 {
					prev = m.Items[len(m.Items)-2]
				}
				if prev != nil && prev.Value != nil && prev.Value.End != nil {
					prev.Value.End = append(prev.Value.End, it.Start...)
					prev.Value.End = append(prev.Value.End, p.sourceToken())
					m.Items = m.Items[:len(m.Items)-1]
					return
				}
			}
			it.Start = append(it.Start, p.sourceToken())
		}
		return
	}
	if p.indent >= m.Indent {
		atMapIndent := !p.onKeyLine && p.indent == m.Indent
		atNextItem := atMapIndent && (it.Sep != nil || it.ExplicitKey) && p.typ != "seq-item-ind"
		start := []*Token{}
		if atNextItem && it.Sep != nil && it.Value == nil {
			var nl []int
			for i, st := range it.Sep {
				switch st.Type {
				case "newline":
					nl = append(nl, i)
				case "space":
				case "comment":
					if st.Indent > m.Indent {
						nl = nl[:0]
					}
				default:
					nl = nl[:0]
				}
			}
			if len(nl) >= 2 {
				start = append([]*Token{}, it.Sep[nl[1]:]...)
				it.Sep = it.Sep[:nl[1]]
			}
		}
		switch p.typ {
		case "anchor", "tag":
			switch {
			case atNextItem || it.Value != nil:
				start = append(start, p.sourceToken())
				m.Items = append(m.Items, &Item{Start: start})
				p.onKeyLine = true
			case it.Sep != nil:
				it.Sep = append(it.Sep, p.sourceToken())
			default:
				it.Start = append(it.Start, p.sourceToken())
			}
			return
		case "explicit-key-ind":
			switch {
			case it.Sep == nil && !it.ExplicitKey:
				it.Start = append(it.Start, p.sourceToken())
				it.ExplicitKey = true
			case atNextItem || it.Value != nil:
				start = append(start, p.sourceToken())
				m.Items = append(m.Items, &Item{Start: start, ExplicitKey: true})
			default:
				p.stack = append(p.stack, p.newBlockMap(&Item{Start: []*Token{p.sourceToken()}, ExplicitKey: true}))
			}
			p.onKeyLine = true
			return
		case "map-value-ind":
			if it.ExplicitKey {
				switch {
				case it.Sep == nil:
					if includesToken(it.Start, "newline") {
						it.Key, it.Sep = nil, []*Token{p.sourceToken()}
					} else {
						s := getFirstKeyStartProps(&it.Start)
						p.stack = append(p.stack, p.newBlockMap(&Item{Start: s, Sep: []*Token{p.sourceToken()}}))
					}
				case it.Value != nil:
					m.Items = append(m.Items, &Item{Start: []*Token{}, Sep: []*Token{p.sourceToken()}})
				case includesToken(it.Sep, "map-value-ind"):
					p.stack = append(p.stack, p.newBlockMap(&Item{Start: start, Sep: []*Token{p.sourceToken()}}))
				case isFlowToken(it.Key) && !includesToken(it.Sep, "newline"):
					s := getFirstKeyStartProps(&it.Start)
					key := it.Key
					sep := append(slices.Clone(it.Sep), p.sourceToken())
					it.Key, it.Sep = nil, nil
					p.stack = append(p.stack, p.newBlockMap(&Item{Start: s, Key: key, Sep: sep}))
				case len(start) > 0:
					it.Sep = append(append(it.Sep, start...), p.sourceToken())
				default:
					it.Sep = append(it.Sep, p.sourceToken())
				}
			} else {
				switch {
				case it.Sep == nil:
					it.Key, it.Sep = nil, []*Token{p.sourceToken()}
				case it.Value != nil || atNextItem:
					m.Items = append(m.Items, &Item{Start: start, Sep: []*Token{p.sourceToken()}})
				case includesToken(it.Sep, "map-value-ind"):
					p.stack = append(p.stack, p.newBlockMap(&Item{Start: []*Token{}, Sep: []*Token{p.sourceToken()}}))
				default:
					it.Sep = append(it.Sep, p.sourceToken())
				}
			}
			p.onKeyLine = true
			return
		case "alias", "scalar", "single-quoted-scalar", "double-quoted-scalar":
			fs := p.flowScalar(p.typ)
			switch {
			case atNextItem || it.Value != nil:
				m.Items = append(m.Items, &Item{Start: start, Key: fs, Sep: []*Token{}})
				p.onKeyLine = true
			case it.Sep != nil:
				p.stack = append(p.stack, fs)
			default:
				it.Key, it.Sep = fs, []*Token{}
				p.onKeyLine = true
			}
			return
		default:
			if bv := p.startBlockValue(m); bv != nil {
				if bv.Type == "block-seq" {
					if !it.ExplicitKey && it.Sep != nil && !includesToken(it.Sep, "newline") {
						p.pop(&Token{Type: "error", Offset: p.offset, Message: "Unexpected block-seq-ind on same line with key", Source: p.source})
						return
					}
				} else if atMapIndent {
					m.Items = append(m.Items, &Item{Start: start})
				}
				p.stack = append(p.stack, bv)
				return
			}
		}
	}
	p.pop(nil)
	p.step()
}

func (p *parser) blockSequence(seq *Token) {
	it := lastItem(seq.Items)
	switch p.typ {
	case "newline":
		if it.Value != nil {
			end := endOf(it.Value)
			if len(end) > 0 && end[len(end)-1].Type == "comment" {
				it.Value.End = append(it.Value.End, p.sourceToken())
			} else {
				seq.Items = append(seq.Items, &Item{Start: []*Token{p.sourceToken()}})
			}
		} else {
			it.Start = append(it.Start, p.sourceToken())
		}
		return
	case "space", "comment":
		if it.Value != nil {
			seq.Items = append(seq.Items, &Item{Start: []*Token{p.sourceToken()}})
		} else {
			if p.atIndentedComment(it.Start, seq.Indent) {
				var prev *Item
				if len(seq.Items) >= 2 {
					prev = seq.Items[len(seq.Items)-2]
				}
				if prev != nil && prev.Value != nil && prev.Value.End != nil {
					prev.Value.End = append(prev.Value.End, it.Start...)
					prev.Value.End = append(prev.Value.End, p.sourceToken())
					seq.Items = seq.Items[:len(seq.Items)-1]
					return
				}
			}
			it.Start = append(it.Start, p.sourceToken())
		}
		return
	case "anchor", "tag":
		if it.Value == nil && p.indent > seq.Indent {
			it.Start = append(it.Start, p.sourceToken())
			return
		}
	case "seq-item-ind":
		if p.indent == seq.Indent {
			if it.Value != nil || includesToken(it.Start, "seq-item-ind") {
				seq.Items = append(seq.Items, &Item{Start: []*Token{p.sourceToken()}})
			} else {
				it.Start = append(it.Start, p.sourceToken())
			}
			return
		}
	}
	if p.indent > seq.Indent {
		if bv := p.startBlockValue(seq); bv != nil {
			p.stack = append(p.stack, bv)
			return
		}
	}
	p.pop(nil)
	p.step()
}

func (p *parser) flowCollection(fc *Token) {
	it := lastItem(fc.Items)
	if p.typ == "flow-error-end" {
		for {
			p.pop(nil)
			top := p.peek(1)
			if top == nil || top.Type != "flow-collection" {
				break
			}
		}
		return
	}
	if len(fc.End) == 0 {
		switch p.typ {
		case "comma", "explicit-key-ind":
			if it == nil || it.Sep != nil {
				fc.Items = append(fc.Items, &Item{Start: []*Token{p.sourceToken()}})
			} else {
				it.Start = append(it.Start, p.sourceToken())
			}
			return
		case "map-value-ind":
			switch {
			case it == nil || it.Value != nil:
				fc.Items = append(fc.Items, &Item{Start: []*Token{}, Sep: []*Token{p.sourceToken()}})
			case it.Sep != nil:
				it.Sep = append(it.Sep, p.sourceToken())
			default:
				it.Key, it.Sep = nil, []*Token{p.sourceToken()}
			}
			return
		case "space", "comment", "newline", "anchor", "tag":
			switch {
			case it == nil || it.Value != nil:
				fc.Items = append(fc.Items, &Item{Start: []*Token{p.sourceToken()}})
			case it.Sep != nil:
				it.Sep = append(it.Sep, p.sourceToken())
			default:
				it.Start = append(it.Start, p.sourceToken())
			}
			return
		case "alias", "scalar", "single-quoted-scalar", "double-quoted-scalar":
			fs := p.flowScalar(p.typ)
			switch {
			case it == nil || it.Value != nil:
				fc.Items = append(fc.Items, &Item{Start: []*Token{}, Key: fs, Sep: []*Token{}})
			case it.Sep != nil:
				p.stack = append(p.stack, fs)
			default:
				it.Key, it.Sep = fs, []*Token{}
			}
			return
		case "flow-map-end", "flow-seq-end":
			fc.End = append(fc.End, p.sourceToken())
			return
		}
		if bv := p.startBlockValue(fc); bv != nil {
			p.stack = append(p.stack, bv)
		} else {
			p.pop(nil)
			p.step()
		}
		return
	}
	parent := p.peek(2)
	switch {
	case parent.Type == "block-map" && ((p.typ == "map-value-ind" && parent.Indent == fc.Indent) || (p.typ == "newline" && lastItem(parent.Items).Sep == nil)):
		p.pop(nil)
		p.step()
	case p.typ == "map-value-ind" && parent.Type != "flow-collection":
		prev := getPrevProps(parent)
		start := getFirstKeyStartProps(prev)
		fixFlowSeqItems(fc)
		sep := append([]*Token{}, fc.End[1:]...)
		fc.End = fc.End[:1]
		sep = append(sep, p.sourceToken())
		m := &Token{Type: "block-map", Offset: fc.Offset, Indent: fc.Indent, Items: []*Item{{Start: start, Key: fc, Sep: sep}}}
		p.onKeyLine = true
		p.stack[len(p.stack)-1] = m
	default:
		p.lineEnd(fc)
	}
}

func (p *parser) flowScalar(typ string) *Token {
	p.newLinesIn(p.source)
	return &Token{Type: typ, Offset: p.offset, Indent: p.indent, Source: p.source}
}

func (p *parser) startBlockValue(parent *Token) *Token {
	switch p.typ {
	case "alias", "scalar", "single-quoted-scalar", "double-quoted-scalar":
		return p.flowScalar(p.typ)
	case "block-scalar-header":
		return &Token{Type: "block-scalar", Offset: p.offset, Indent: p.indent, Props: []*Token{p.sourceToken()}, Source: ""}
	case "flow-map-start", "flow-seq-start":
		return &Token{Type: "flow-collection", Offset: p.offset, Indent: p.indent, StartTok: p.sourceToken(), Items: []*Item{}, End: []*Token{}}
	case "seq-item-ind":
		return &Token{Type: "block-seq", Offset: p.offset, Indent: p.indent, Items: []*Item{{Start: []*Token{p.sourceToken()}}}}
	case "explicit-key-ind":
		p.onKeyLine = true
		start := getFirstKeyStartProps(getPrevProps(parent))
		start = append(start, p.sourceToken())
		return p.newBlockMap(&Item{Start: start, ExplicitKey: true})
	case "map-value-ind":
		p.onKeyLine = true
		start := getFirstKeyStartProps(getPrevProps(parent))
		return p.newBlockMap(&Item{Start: start, Sep: []*Token{p.sourceToken()}})
	}
	return nil
}

func (p *parser) atIndentedComment(start []*Token, indent int) bool {
	if p.typ != "comment" || p.indent <= indent {
		return false
	}
	for _, st := range start {
		if st.Type != "newline" && st.Type != "space" {
			return false
		}
	}
	return true
}

func (p *parser) documentEnd(docEnd *Token) {
	if p.typ != "doc-mode" {
		if docEnd.End != nil {
			docEnd.End = append(docEnd.End, p.sourceToken())
		} else {
			docEnd.End = []*Token{p.sourceToken()}
		}
		if p.typ == "newline" {
			p.pop(nil)
		}
	}
}

func (p *parser) lineEnd(token *Token) {
	switch p.typ {
	case "comma", "doc-start", "doc-end", "flow-seq-end", "flow-map-end", "map-value-ind":
		p.pop(nil)
		p.step()
		return
	case "newline":
		p.onKeyLine = false
	}
	if token.End != nil {
		token.End = append(token.End, p.sourceToken())
	} else {
		token.End = []*Token{p.sourceToken()}
	}
	if p.typ == "newline" {
		p.pop(nil)
	}
}
