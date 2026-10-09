package yaml12

// Ports yaml 2.9.0 src/compose/{composer,compose-doc,compose-node,compose-scalar,compose-collection,resolve-block-map,resolve-block-seq,resolve-flow-collection}.ts with the core schema of src/schema/core. Copyright Eemeli Aro, ISC licence (see lexer.go). Comment text and node spacing, which never reach a parsed value or an error, are not kept.

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

type nodeKind int

const (
	kindScalar nodeKind = iota
	kindMap
	kindSeq
	kindAlias
)

type node struct {
	kind  nodeKind
	value any
	items []*node // sequence items
	pairs []*pair // map items
	// class names the collection type: map, seq, set, omap or pairs.
	class  string
	anchor string
	// source is an alias's name, or a scalar's resolved text.
	source string
	rng    [3]int
	// tag is the explicit tag, as the library keeps it in node.tag.
	tag string
	// format is the number format of the tag that resolved the scalar (OCT, HEX, EXP), and minFractionDigits the decimals a float was written with.
	format            string
	minFractionDigits int
	typ               string
	flow              bool
	mergeKey          bool
	comment           string
	commentBefore     string
	spaceBefore       bool
	// pairsItems marks a sequence whose items are pairs (!!omap, !!pairs): items hold keys in pairs.
}

type pair struct{ key, value *node }

func (n *node) hasAnchor() bool { return n != nil && n.kind != kindAlias && n.anchor != "" }

type composeCtx struct {
	atRoot bool
	dirs   *directives
	// pushed names the known tags this document has used; the library appends a copy of each to its schema the first time.
	pushed map[string]bool
}

type coreTag struct {
	tag    string
	format string
	test   *lazyregexp.Regexp
	// resolve gives the value, and the decimals a float was written with.
	resolve func(string) (any, int)
}

func plain(f func(string) any) func(string) (any, int) {
	return func(s string) (any, int) { return f(s), 0 }
}

var coreScalarTags = []coreTag{
	{tag: "tag:yaml.org,2002:null", test: lazyregexp.New(`^(?:~|[Nn]ull|NULL)?$`), resolve: plain(func(string) any { return nil })},
	{tag: "tag:yaml.org,2002:bool", test: lazyregexp.New(`^(?:[Tt]rue|TRUE|[Ff][a]lse|FALSE)$`), resolve: plain(func(s string) any { return s[0] == 't' || s[0] == 'T' })},
	{tag: "tag:yaml.org,2002:int", format: "OCT", test: lazyregexp.New(`^0o[0-7]+$`), resolve: plain(func(s string) any { return parseJSInt(s[2:], 8) })},
	{tag: "tag:yaml.org,2002:int", test: lazyregexp.New(`^[-+]?[0-9]+$`), resolve: plain(func(s string) any { return parseJSInt(s, 10) })},
	{tag: "tag:yaml.org,2002:int", format: "HEX", test: lazyregexp.New(`^0x[0-9a-fA-F]+$`), resolve: plain(func(s string) any { return parseJSInt(s[2:], 16) })},
	{tag: "tag:yaml.org,2002:float", test: lazyregexp.New(`^(?:[-+]?\.(?:inf|Inf|INF)|\.nan|\.NaN|\.NAN)$`), resolve: plain(func(s string) any {
		switch {
		case strings.ToLower(s[len(s)-3:]) == "nan":
			return math.NaN()
		case s[0] == '-':
			return math.Inf(-1)
		}
		return math.Inf(1)
	})},
	{tag: "tag:yaml.org,2002:float", format: "EXP", test: lazyregexp.New(`^[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)[eE][-+]?[0-9]+$`), resolve: plain(parseJSFloat)},
	{tag: "tag:yaml.org,2002:float", test: lazyregexp.New(`^[-+]?(?:\.[0-9]+|[0-9]+\.[0-9]*)$`), resolve: func(s string) (any, int) {
		minFrac := 0
		if dot := strings.IndexByte(s, '.'); dot != -1 && s[len(s)-1] == '0' {
			minFrac = len(s) - dot - 1
		}
		return parseJSFloat(s), minFrac
	}},
}

const (
	strTag = "tag:yaml.org,2002:str"
	mapTag = "tag:yaml.org,2002:map"
	seqTag = "tag:yaml.org,2002:seq"
)

func parseJSFloat(s string) any {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// parseJSInt is parseInt(digits, radix) for a string of sign and digits valid in the radix.
func parseJSInt(s string, radix int) any {
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n, _ := new(big.Int).SetString(s, radix)
	f, _ := new(big.Float).SetInt(n).Float64()
	if neg {
		f = -f
	}
	return f
}

type document struct {
	comment, commentBefore string
	pushed                 map[string]bool
	contents               *node
	errors                 []*yamlError
	warnings               []*yamlError
	dirs                   *directives
	rng                    [3]int
}

type composer struct {
	doc         *document
	atDirective bool
	errors      []*yamlError
	warnings    []*yamlError
	dirs        *directives
	prelude     []string
}

// parsePrelude gathers the comments between documents the way the library's Composer does.
func parsePrelude(prelude []string) (comment string, afterEmptyLine bool) {
	atComment := false
	for i := 0; i < len(prelude); i++ {
		source := prelude[i]
		switch {
		case source != "" && source[0] == '#':
			sep := ""
			if comment != "" {
				sep = "\n"
				if afterEmptyLine {
					sep = "\n\n"
				}
			}
			text := source[1:]
			if text == "" {
				text = " "
			}
			comment += sep + text
			atComment = true
			afterEmptyLine = false
		case source != "" && source[0] == '%':
			if i+1 >= len(prelude) || prelude[i+1] == "" || prelude[i+1][0] != '#' {
				i++
			}
			atComment = false
		default:
			if !atComment {
				afterEmptyLine = true
			}
			atComment = false
		}
	}
	return comment, afterEmptyLine
}

func newComposer() *composer {
	return &composer{dirs: newDirectives("1.2")}
}

func (c *composer) onError(p pos, code, message string, warning ...bool) {
	e := &yamlError{code: code, pos: p, message: message}
	if len(warning) > 0 && warning[0] {
		c.warnings = append(c.warnings, e)
	} else {
		c.errors = append(c.errors, e)
	}
}

func prependComment(existing *string, comment string) {
	if *existing != "" {
		*existing = comment + "\n" + *existing
	} else {
		*existing = comment
	}
}

func (c *composer) decorate(doc *document, afterDoc bool) {
	if comment, afterEmptyLine := parsePrelude(c.prelude); comment != "" {
		dc := doc.contents
		switch {
		case afterDoc:
			if doc.comment != "" {
				doc.comment += "\n" + comment
			} else {
				doc.comment = comment
			}
		case afterEmptyLine || doc.dirs.docStart || dc == nil:
			doc.commentBefore = comment
		case (dc.kind == kindMap || dc.kind == kindSeq) && !dc.flow && len(dc.items)+len(dc.pairs) > 0:
			if len(dc.pairs) > 0 {
				prependComment(&dc.pairs[0].key.commentBefore, comment)
			} else {
				prependComment(&dc.items[0].commentBefore, comment)
			}
		default:
			prependComment(&dc.commentBefore, comment)
		}
	}
	c.prelude = nil
	if afterDoc {
		doc.errors = append(doc.errors, c.errors...)
		doc.warnings = append(doc.warnings, c.warnings...)
	} else {
		doc.errors = c.errors
		doc.warnings = c.warnings
	}
	c.errors, c.warnings = nil, nil
}

// next consumes a CST token and returns the document it completes, if any.
func (c *composer) next(token *Token) *document {
	switch token.Type {
	case "directive":
		c.dirs.add(token.Source, func(offset int, message string, warning bool) {
			p := posOfToken(token)
			p[0] += offset
			c.onError(p, "BAD_DIRECTIVE", message, warning)
		})
		c.prelude = append(c.prelude, token.Source)
		c.atDirective = true
	case "document":
		doc := composeDoc(c.dirs, token, c.onError)
		if c.atDirective && !doc.dirs.docStart {
			c.onError(posOfToken(token), "MISSING_CHAR", "Missing directives-end/doc-start indicator line")
		}
		c.decorate(doc, false)
		prev := c.doc
		c.doc = doc
		c.atDirective = false
		return prev
	case "byte-order-mark", "space":
	case "comment", "newline":
		c.prelude = append(c.prelude, token.Source)
	case "error":
		msg := token.Message
		if token.Source != "" {
			msg += ": " + jsonQuote(token.Source)
		}
		e := &yamlError{code: "UNEXPECTED_TOKEN", pos: posOfToken(token), message: msg}
		if c.atDirective || c.doc == nil {
			c.errors = append(c.errors, e)
		} else {
			c.doc.errors = append(c.doc.errors, e)
		}
	case "doc-end":
		if c.doc == nil {
			c.errors = append(c.errors, &yamlError{code: "UNEXPECTED_TOKEN", pos: posOfToken(token), message: "Unexpected doc-end without preceding document"})
			break
		}
		c.doc.dirs.docEnd = true
		end, comment := resolveEnd(token.End, token.Offset+len(token.Source), true, c.onError)
		c.decorate(c.doc, true)
		if comment != "" {
			if c.doc.comment != "" {
				c.doc.comment += "\n" + comment
			} else {
				c.doc.comment = comment
			}
		}
		c.doc.rng[2] = end
	default:
		c.errors = append(c.errors, &yamlError{code: "UNEXPECTED_TOKEN", pos: posOfToken(token), message: "Unsupported token " + token.Type})
	}
	return nil
}

// end flushes the last document; with forceDoc it returns an empty document when there is none.
func (c *composer) end(forceDoc bool, endOffset int) *document {
	if c.doc != nil {
		c.decorate(c.doc, true)
		doc := c.doc
		c.doc = nil
		return doc
	}
	if forceDoc {
		doc := &document{dirs: c.dirs.atDocument()}
		if c.atDirective {
			c.onError(posOfOffset(endOffset), "MISSING_CHAR", "Missing directives-end indicator line")
		}
		doc.rng = [3]int{0, endOffset, endOffset}
		c.decorate(doc, false)
		return doc
	}
	return nil
}

func composeDoc(dirs *directives, token *Token, onError errorFn) *document {
	doc := &document{dirs: dirs.atDocument()}
	ctx := &composeCtx{atRoot: true, dirs: doc.dirs, pushed: map[string]bool{}}
	doc.pushed = ctx.pushed
	var next *Token
	if token.Value != nil {
		next = token.Value
	} else if len(token.End) > 0 {
		next = token.End[0]
	}
	props := resolveProps(token.Start, propsOptions{indicator: "doc-start", next: next, offset: token.Offset, startOnNL: true}, onError)
	if props.found != nil {
		doc.dirs.docStart = true
		if token.Value != nil && (token.Value.Type == "block-map" || token.Value.Type == "block-seq") && !props.hasNewline {
			onError(posOfOffset(props.end), "MISSING_CHAR", "Block collection cannot start on same line with directives-end marker")
		}
	}
	if token.Value != nil {
		doc.contents = composeNode(ctx, token.Value, props, onError)
	} else {
		doc.contents = composeEmptyNode(ctx, props.end, token.Start, props, onError)
	}
	contentEnd := doc.contents.rng[2]
	end, _ := resolveEnd(token.End, contentEnd, false, onError)
	doc.rng = [3]int{token.Offset, contentEnd, end}
	return doc
}

func composeNode(ctx *composeCtx, token *Token, props propsResult, onError errorFn) *node {
	var n *node
	switch token.Type {
	case "alias":
		n = composeAlias(token, onError)
		if props.anchor != nil || props.tag != nil {
			onError(posOfToken(token), "ALIAS_PROPS", "An alias node must not specify any properties")
		}
	case "scalar", "single-quoted-scalar", "double-quoted-scalar", "block-scalar":
		n = composeScalar(ctx, token, props.tag, onError)
		if props.anchor != nil {
			n.anchor = props.anchor.Source[1:]
		}
	case "block-map", "block-seq", "flow-collection":
		n = composeCollection(ctx, token, props, onError)
		if props.anchor != nil {
			n.anchor = props.anchor.Source[1:]
		}
	default:
		message := "Unsupported token (type: " + token.Type + ")"
		if token.Type == "error" {
			message = token.Message
		}
		onError(posOfToken(token), "UNEXPECTED_TOKEN", message)
	}
	if n == nil {
		n = composeEmptyNode(ctx, token.Offset, nil, props, onError)
	}
	if props.anchor != nil && n.anchor == "" && n.kind != kindAlias {
		onError(posOfToken(props.anchor), "BAD_ALIAS", "Anchor cannot be an empty string")
	}
	if props.spaceBefore {
		n.spaceBefore = true
	}
	if props.comment != "" {
		if token.Type == "scalar" && token.Source == "" {
			n.comment = props.comment
		} else {
			n.commentBefore = props.comment
		}
	}
	return n
}

func composeEmptyNode(ctx *composeCtx, offset int, before []*Token, props propsResult, onError errorFn) *node {
	token := &Token{Type: "scalar", Offset: emptyScalarPosition(offset, before), Indent: -1, Source: ""}
	n := composeScalar(ctx, token, props.tag, onError)
	if props.anchor != nil {
		n.anchor = props.anchor.Source[1:]
		if n.anchor == "" {
			onError(posOfToken(props.anchor), "BAD_ALIAS", "Anchor cannot be an empty string")
		}
	}
	if props.spaceBefore {
		n.spaceBefore = true
	}
	if props.comment != "" {
		n.comment = props.comment
		n.rng[2] = props.end
	}
	return n
}

func composeAlias(token *Token, onError errorFn) *node {
	source := token.Source[1:]
	n := &node{kind: kindAlias, source: source}
	if source == "" {
		onError(posOfOffset(token.Offset), "BAD_ALIAS", "Alias cannot be an empty string")
	}
	if strings.HasSuffix(source, ":") {
		onError(posOfOffset(token.Offset+len(token.Source)-1), "BAD_ALIAS", "Alias ending in : is ambiguous", true)
	}
	valueEnd := token.Offset + len(token.Source)
	end, comment := resolveEnd(token.End, valueEnd, true, onError)
	n.rng = [3]int{token.Offset, valueEnd, end}
	n.comment = comment
	return n
}

func composeScalar(ctx *composeCtx, token *Token, tagToken *Token, onError errorFn) *node {
	var res scalarResult
	if token.Type == "block-scalar" {
		res = resolveBlockScalar(ctx.atRoot, true, token, onError)
	} else {
		res = resolveFlowScalar(token, true, onError)
	}
	tagName := ""
	if tagToken != nil {
		tagName = ctx.dirs.tagName(tagToken.Source, func(msg string) { onError(posOfToken(tagToken), "TAG_RESOLVE_FAILED", msg) })
	}
	out := scalarOutcome{value: res.value}
	switch {
	case tagName != "":
		out = resolveScalarByName(ctx, res.value, tagName, tagToken, onError)
	case token.Type == "scalar":
		for _, t := range coreScalarTags {
			if t.test.MatchString(res.value) {
				value, minFrac := t.resolve(res.value)
				out = scalarOutcome{value: value, format: t.format, minFrac: minFrac}
				break
			}
		}
	}
	n := &node{kind: kindScalar, value: out.value, rng: res.rng, source: res.value, typ: res.typ, comment: res.comment, format: out.format, minFractionDigits: out.minFrac, mergeKey: out.mergeKey}
	if tagName != "" {
		n.tag = tagName
	}
	return n
}

type scalarOutcome struct {
	value    any
	format   string
	minFrac  int
	mergeKey bool
}

// resolveScalarByName follows findScalarTagByName: a tag whose test fails, or an unknown one, leaves the string and warns.
func resolveScalarByName(ctx *composeCtx, value, tagName string, tagToken *Token, onError errorFn) scalarOutcome {
	if tagName == "!" || tagName == strTag {
		return scalarOutcome{value: value}
	}
	for _, t := range coreScalarTags {
		if t.tag == tagName && t.test.MatchString(value) {
			v, minFrac := t.resolve(value)
			return scalarOutcome{value: v, format: t.format, minFrac: minFrac}
		}
	}
	if out, ok := resolveKnownScalarTag(ctx, value, tagName, tagToken, onError); ok {
		return out
	}
	onError(posOfToken(tagToken), "TAG_RESOLVE_FAILED", "Unresolved tag: "+tagName, tagName != strTag)
	return scalarOutcome{value: value}
}

func composeCollection(ctx *composeCtx, token *Token, props propsResult, onError errorFn) *node {
	tagToken := props.tag
	tagName := ""
	if tagToken != nil {
		tagName = ctx.dirs.tagName(tagToken.Source, func(msg string) { onError(posOfToken(tagToken), "TAG_RESOLVE_FAILED", msg) })
	}
	if token.Type == "block-seq" {
		anchor, nl := props.anchor, props.newlineAfterProp
		var lastProp *Token
		switch {
		case anchor != nil && tagToken != nil:
			lastProp = tagToken
			if anchor.Offset > tagToken.Offset {
				lastProp = anchor
			}
		case anchor != nil:
			lastProp = anchor
		default:
			lastProp = tagToken
		}
		if lastProp != nil && (nl == nil || nl.Offset < lastProp.Offset) {
			onError(posOfToken(lastProp), "MISSING_CHAR", "Missing newline after block sequence props")
		}
	}
	expType := "seq"
	if token.Type == "block-map" || (token.Type == "flow-collection" && token.StartTok.Source == "{") {
		expType = "map"
	}
	if tagToken == nil || tagName == "" || tagName == "!" || (tagName == mapTag && expType == "map") || (tagName == seqTag && expType == "seq") {
		return resolveCollection(ctx, token, onError, tagName, "")
	}
	known := knownCollectionTag(tagName, expType)
	if known == "" {
		if kt, ok := knownTagCollectionKind(tagName); ok {
			expects := kt
			if expects == "" {
				expects = "scalar"
			}
			onError(posOfToken(tagToken), "BAD_COLLECTION_TYPE", tagName+" used for "+expType+" collection, but expects "+expects, true)
		} else {
			onError(posOfToken(tagToken), "TAG_RESOLVE_FAILED", "Unresolved tag: "+tagName, true)
		}
		return resolveCollection(ctx, token, onError, tagName, "")
	}
	ctx.pushed[tagName] = true
	coll := resolveCollection(ctx, token, onError, tagName, known)
	resolveKnownCollection(coll, known, func(msg string) { onError(posOfToken(tagToken), "TAG_RESOLVE_FAILED", msg) })
	coll.tag = tagName
	return coll
}

// resolveCollection builds the collection a CST collection token stands for. A tag the schema knows names the node class (set, omap, pairs).
func resolveCollection(ctx *composeCtx, token *Token, onError errorFn, tagName, class string) *node {
	var coll *node
	switch token.Type {
	case "block-map":
		coll = resolveBlockMap(ctx, token, onError)
	case "block-seq":
		coll = resolveBlockSeq(ctx, token, onError)
	default:
		coll = resolveFlowCollection(ctx, token, onError)
	}
	own := mapTag
	if coll.kind == kindSeq {
		own = seqTag
	}
	if tagName == "!" || tagName == own {
		coll.tag = own
	} else if tagName != "" {
		coll.tag = tagName
	}
	return coll
}

// mapIncludes is util-map-includes.ts with the default uniqueKeys: the same key node, or two scalars whose values are === in JavaScript.
func mapIncludes(items []*pair, search *node) bool {
	for _, p := range items {
		a, b := p.key, search
		if a == b || (a.kind == kindScalar && b.kind == kindScalar && jsStrictEquals(a.value, b.value, false)) {
			return true
		}
	}
	return false
}

func hasIndent(t *Token) bool {
	return t.Type != "error" && t.Type != "document" && t.Type != "doc-end"
}

const startColMsg = "All mapping items must start at the same column"

func resolveBlockMap(ctx *composeCtx, bm *Token, onError errorFn) *node {
	m := &node{kind: kindMap, class: "map"}
	if ctx.atRoot {
		ctx.atRoot = false
	}
	offset := bm.Offset
	commentEnd := -1
	for _, item := range bm.Items {
		start, key, sep, value := item.Start, item.Key, item.Sep, item.Value
		var next *Token
		if key != nil {
			next = key
		} else if len(sep) > 0 {
			next = sep[0]
		}
		keyProps := resolveProps(start, propsOptions{indicator: "explicit-key-ind", next: next, offset: offset, parentIndent: bm.Indent, startOnNL: true}, onError)
		implicitKey := keyProps.found == nil
		if implicitKey {
			if key != nil {
				if key.Type == "block-seq" {
					onError(posOfOffset(offset), "BLOCK_AS_IMPLICIT_KEY", "A block sequence may not be used as an implicit map key")
				} else if hasIndent(key) && key.Indent != bm.Indent {
					onError(posOfOffset(offset), "BAD_INDENT", startColMsg)
				}
			}
			if keyProps.anchor == nil && keyProps.tag == nil && sep == nil {
				commentEnd = keyProps.end
				if keyProps.comment != "" {
					if m.comment != "" {
						m.comment += "\n" + keyProps.comment
					} else {
						m.comment = keyProps.comment
					}
				}
				continue
			}
			if keyProps.newlineAfterProp != nil || containsNewline(key) {
				p := pos{}
				if key != nil {
					p = posOfToken(key)
				} else if len(start) > 0 {
					p = posOfToken(start[len(start)-1])
				}
				onError(p, "MULTILINE_IMPLICIT_KEY", "Implicit keys need to be on a single line")
			}
		} else if keyProps.found.Indent != bm.Indent {
			onError(posOfOffset(offset), "BAD_INDENT", startColMsg)
		}
		keyStart := keyProps.end
		var keyNode *node
		if key != nil {
			keyNode = composeNode(ctx, key, keyProps, onError)
		} else {
			keyNode = composeEmptyNode(ctx, keyStart, start, keyProps, onError)
		}
		if mapIncludes(m.pairs, keyNode) {
			onError(posOfOffset(keyStart), "DUPLICATE_KEY", "Map keys must be unique")
		}
		valueProps := resolveProps(sep, propsOptions{indicator: "map-value-ind", next: value, offset: keyNode.rng[2], parentIndent: bm.Indent, startOnNL: key == nil || key.Type == "block-scalar"}, onError)
		offset = valueProps.end
		if valueProps.found != nil {
			if implicitKey {
				if value != nil && value.Type == "block-map" && !valueProps.hasNewline {
					onError(posOfOffset(offset), "BLOCK_AS_IMPLICIT_KEY", "Nested mappings are not allowed in compact mappings")
				}
				if keyProps.start < valueProps.found.Offset-1024 {
					onError(posOfRange(keyNode.rng), "KEY_OVER_1024_CHARS", "The : indicator must be at most 1024 chars after the start of an implicit block mapping key")
				}
			}
			var valueNode *node
			if value != nil {
				valueNode = composeNode(ctx, value, valueProps, onError)
			} else {
				valueNode = composeEmptyNode(ctx, offset, sep, valueProps, onError)
			}
			offset = valueNode.rng[2]
			m.pairs = append(m.pairs, &pair{keyNode, valueNode})
		} else {
			if implicitKey {
				onError(posOfRange(keyNode.rng), "MISSING_CHAR", "Implicit map keys need to be followed by map values")
			}
			if valueProps.comment != "" {
				if keyNode.comment != "" {
					keyNode.comment += "\n" + valueProps.comment
				} else {
					keyNode.comment = valueProps.comment
				}
			}
			m.pairs = append(m.pairs, &pair{keyNode, nil})
		}
	}
	if commentEnd >= 0 && commentEnd < offset {
		onError(posOfOffset(commentEnd), "IMPOSSIBLE", "Map comment with trailing content")
	}
	ce := offset
	if commentEnd >= 0 {
		ce = commentEnd
	}
	m.rng = [3]int{bm.Offset, offset, ce}
	return m
}

func resolveBlockSeq(ctx *composeCtx, bs *Token, onError errorFn) *node {
	seq := &node{kind: kindSeq, class: "seq"}
	if ctx.atRoot {
		ctx.atRoot = false
	}
	offset := bs.Offset
	commentEnd := -1
	for _, item := range bs.Items {
		start, value := item.Start, item.Value
		props := resolveProps(start, propsOptions{indicator: "seq-item-ind", next: value, offset: offset, parentIndent: bs.Indent, startOnNL: true}, onError)
		if props.found == nil {
			if props.anchor != nil || props.tag != nil || value != nil {
				if value != nil && value.Type == "block-seq" {
					onError(posOfOffset(props.end), "BAD_INDENT", "All sequence items must start at the same column")
				} else {
					onError(posOfOffset(offset), "MISSING_CHAR", "Sequence item without - indicator")
				}
			} else {
				commentEnd = props.end
				if props.comment != "" {
					seq.comment = props.comment
				}
				continue
			}
		}
		var n *node
		if value != nil {
			n = composeNode(ctx, value, props, onError)
		} else {
			n = composeEmptyNode(ctx, props.end, start, props, onError)
		}
		offset = n.rng[2]
		seq.items = append(seq.items, n)
	}
	ce := offset
	if commentEnd >= 0 {
		ce = commentEnd
	}
	seq.rng = [3]int{bs.Offset, offset, ce}
	return seq
}

const blockMsg = "Block collections are not allowed within flow collections"

func isBlock(t *Token) bool { return t != nil && (t.Type == "block-map" || t.Type == "block-seq") }

func resolveFlowCollection(ctx *composeCtx, fc *Token, onError errorFn) *node {
	isMap := fc.StartTok.Source == "{"
	fcName := "flow sequence"
	if isMap {
		fcName = "flow map"
	}
	coll := &node{kind: kindSeq, class: "seq", flow: true}
	if isMap {
		coll.kind, coll.class = kindMap, "map"
	}
	atRoot := ctx.atRoot
	if atRoot {
		ctx.atRoot = false
	}
	offset := fc.Offset + len(fc.StartTok.Source)
	for i, item := range fc.Items {
		start, key, sep, value := item.Start, item.Key, item.Sep, item.Value
		var next *Token
		if key != nil {
			next = key
		} else if len(sep) > 0 {
			next = sep[0]
		}
		props := resolveProps(start, propsOptions{flow: fcName, indicator: "explicit-key-ind", next: next, offset: offset, parentIndent: fc.Indent}, onError)
		if props.found == nil {
			if props.anchor == nil && props.tag == nil && sep == nil && value == nil {
				if i == 0 && props.comma != nil {
					onError(posOfToken(props.comma), "UNEXPECTED_TOKEN", "Unexpected , in "+fcName)
				} else if i < len(fc.Items)-1 {
					onError(posOfOffset(props.start), "UNEXPECTED_TOKEN", "Unexpected empty item in "+fcName)
				}
				if props.comment != "" {
					if coll.comment != "" {
						coll.comment += "\n" + props.comment
					} else {
						coll.comment = props.comment
					}
				}
				offset = props.end
				continue
			}
			if !isMap && containsNewline(key) {
				onError(posOfToken(key), "MULTILINE_IMPLICIT_KEY", "Implicit keys of flow sequence pairs need to be on a single line")
			}
		}
		if i == 0 {
			if props.comma != nil {
				onError(posOfToken(props.comma), "UNEXPECTED_TOKEN", "Unexpected , in "+fcName)
			}
		} else {
			if props.comma == nil {
				onError(posOfOffset(props.start), "MISSING_CHAR", "Missing , between "+fcName+" items")
			}
			if props.comment != "" {
				prevItemComment := ""
			scan:
				for _, st := range start {
					switch st.Type {
					case "comma", "space":
					case "comment":
						prevItemComment = st.Source[1:]
						break scan
					default:
						break scan
					}
				}
				if prevItemComment != "" {
					var prev *node
					switch {
					case isMap:
						if last := coll.pairs[len(coll.pairs)-1]; last.value != nil {
							prev = last.value
						} else {
							prev = last.key
						}
					case len(coll.items) > 0:
						prev = coll.items[len(coll.items)-1]
					}
					if prev != nil {
						if prev.comment != "" {
							prev.comment += "\n" + prevItemComment
						} else {
							prev.comment = prevItemComment
						}
					}
					props.comment = props.comment[min(len(prevItemComment)+1, len(props.comment)):]
				}
			}
		}
		if !isMap && sep == nil && props.found == nil {
			var valueNode *node
			if value != nil {
				valueNode = composeNode(ctx, value, props, onError)
			} else {
				valueNode = composeEmptyNode(ctx, props.end, sep, props, onError)
			}
			coll.items = append(coll.items, valueNode)
			offset = valueNode.rng[2]
			if isBlock(value) {
				onError(posOfRange(valueNode.rng), "BLOCK_IN_FLOW", blockMsg)
			}
			continue
		}
		keyStart := props.end
		var keyNode *node
		if key != nil {
			keyNode = composeNode(ctx, key, props, onError)
		} else {
			keyNode = composeEmptyNode(ctx, keyStart, start, props, onError)
		}
		if isBlock(key) {
			onError(posOfRange(keyNode.rng), "BLOCK_IN_FLOW", blockMsg)
		}
		valueProps := resolveProps(sep, propsOptions{flow: fcName, indicator: "map-value-ind", next: value, offset: keyNode.rng[2], parentIndent: fc.Indent}, onError)
		if valueProps.found != nil {
			if !isMap && props.found == nil {
				for _, st := range sep {
					if st == valueProps.found {
						break
					}
					if st.Type == "newline" {
						onError(posOfToken(st), "MULTILINE_IMPLICIT_KEY", "Implicit keys of flow sequence pairs need to be on a single line")
						break
					}
				}
				if props.start < valueProps.found.Offset-1024 {
					onError(posOfToken(valueProps.found), "KEY_OVER_1024_CHARS", "The : indicator must be at most 1024 chars after the start of an implicit flow sequence key")
				}
			}
		} else if value != nil {
			if value.Source != "" && value.Source[0] == ':' && hasSource(value) {
				onError(posOfToken(value), "MISSING_CHAR", "Missing space after : in "+fcName)
			} else {
				onError(posOfOffset(valueProps.start), "MISSING_CHAR", "Missing , or : between "+fcName+" items")
			}
		}
		var valueNode *node
		if value != nil {
			valueNode = composeNode(ctx, value, valueProps, onError)
		} else if valueProps.found != nil {
			valueNode = composeEmptyNode(ctx, valueProps.end, sep, valueProps, onError)
		}
		if valueNode != nil {
			if isBlock(value) {
				onError(posOfRange(valueNode.rng), "BLOCK_IN_FLOW", blockMsg)
			}
		} else if valueProps.comment != "" {
			if keyNode.comment != "" {
				keyNode.comment += "\n" + valueProps.comment
			} else {
				keyNode.comment = valueProps.comment
			}
		}
		pr := &pair{keyNode, valueNode}
		if isMap {
			if mapIncludes(coll.pairs, keyNode) {
				onError(posOfOffset(keyStart), "DUPLICATE_KEY", "Map keys must be unique")
			}
			coll.pairs = append(coll.pairs, pr)
		} else {
			endRange := keyNode.rng
			if valueNode != nil {
				endRange = valueNode.rng
			}
			coll.items = append(coll.items, &node{kind: kindMap, class: "map", flow: true, pairs: []*pair{pr}, rng: [3]int{keyNode.rng[0], endRange[1], endRange[2]}})
		}
		if valueNode != nil {
			offset = valueNode.rng[2]
		} else {
			offset = valueProps.end
		}
	}
	expectedEnd := "]"
	if isMap {
		expectedEnd = "}"
	}
	var ce *Token
	var ee []*Token
	if len(fc.End) > 0 {
		ce, ee = fc.End[0], append([]*Token{}, fc.End[1:]...)
	}
	cePos := offset
	if ce != nil && ce.Source == expectedEnd {
		cePos = ce.Offset + len(ce.Source)
	} else {
		name := strings.ToUpper(fcName[:1]) + fcName[1:]
		code, msg := "BAD_INDENT", name+" in block collection must be sufficiently indented and end with a "+expectedEnd
		if atRoot {
			code, msg = "MISSING_CHAR", name+" must end with a "+expectedEnd
		}
		onError(posOfOffset(offset), code, msg)
		if ce != nil && len(ce.Source) != 1 {
			ee = append([]*Token{ce}, ee...)
		}
	}
	if len(ee) > 0 {
		end, comment := resolveEnd(ee, cePos, true, onError)
		if comment != "" {
			if coll.comment != "" {
				coll.comment += "\n" + comment
			} else {
				coll.comment = comment
			}
		}
		coll.rng = [3]int{fc.Offset, cePos, end}
	} else {
		coll.rng = [3]int{fc.Offset, cePos, cePos}
	}
	return coll
}

// hasSource reports whether the token type carries a source string; the library tests `'source' in value`.
func hasSource(t *Token) bool {
	switch t.Type {
	case "block-map", "block-seq", "flow-collection", "document":
		return false
	}
	return true
}

// jsStrictEquals is JavaScript's === between two scalar values (sameValueZero: Array.prototype.includes, where NaN equals NaN). A Buffer, Date, Set, Map or Symbol is an object or symbol of its own for each node, so it equals no other value.
func jsStrictEquals(a, b any, sameValueZero bool) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && (x == y || (sameValueZero && math.IsNaN(x) && math.IsNaN(y)))
	}
	return false
}
