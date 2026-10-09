package yaml12

// Ports yaml 2.9.0 src/public-api.ts parse, src/doc/Document.ts toJS, src/nodes/{toJS,Alias,addPairToJSMap}.ts and the toJSON methods of the core nodes. Copyright Eemeli Aro, ISC licence (see lexer.go).

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

type anchorData struct {
	aliasCount, count int
	res               any
	done              bool
}

type toJSCtx struct {
	anchors    map[*node]*anchorData
	doc        *document
	aliasNodes []*node
	cached     bool
}

const maxAliasCount = 100

// toJS builds the JavaScript value of a node as the library does. A nil node is null.
func (c *toJSCtx) toJS(n *node) (any, error) {
	if n == nil {
		return nil, nil
	}
	if !n.hasAnchor() {
		return c.toJSON(n, nil)
	}
	data := &anchorData{count: 1}
	c.anchors[n] = data
	res, err := c.toJSON(n, data)
	if err != nil {
		return nil, err
	}
	data.res, data.done = res, true
	return res, nil
}

// toJSON builds a node's value. An anchored collection is recorded in created before its members are built (the library's ctx.onCreate), so an alias to it from inside reads the collection itself and the value is cyclic, as in JavaScript.
func (c *toJSCtx) toJSON(n *node, created *anchorData) (any, error) {
	switch {
	case n.kind == kindScalar:
		return n.value, nil
	case n.kind == kindAlias:
		return c.aliasToJSON(n)
	case n.class == "set":
		set := Set{}
		for _, p := range n.pairs {
			key, err := c.toJS(p.key)
			if err != nil {
				return nil, err
			}
			if !setHas(set.Values, key) {
				set.Values = append(set.Values, key)
			}
		}
		return set, nil
	case n.class == "omap":
		m := Map{}
		for _, p := range n.pairs {
			key, err := c.toJS(p.key)
			if err != nil {
				return nil, err
			}
			value, err := c.toJS(p.value)
			if err != nil {
				return nil, err
			}
			if setHas(m.Keys, key) {
				return nil, errors.New("Ordered maps must not include duplicate keys")
			}
			m.Keys, m.Values = append(m.Keys, key), append(m.Values, value)
		}
		return m, nil
	case n.class == "pairs":
		seq := make([]any, 0, len(n.pairs))
		for _, p := range n.pairs {
			obj := &jsObject{m: map[string]any{}}
			if err := c.addPair(obj, p); err != nil {
				return nil, err
			}
			seq = append(seq, obj.m)
		}
		return seq, nil
	case n.kind == kindSeq:
		seq := make([]any, len(n.items))
		if created != nil {
			created.res, created.done = seq, true
		}
		for i, item := range n.items {
			v, err := c.toJS(item)
			if err != nil {
				return nil, err
			}
			seq[i] = v
		}
		return seq, nil
	}
	obj := &jsObject{m: make(map[string]any, len(n.pairs))}
	if created != nil {
		created.res, created.done = obj.m, true
	}
	for _, p := range n.pairs {
		if err := c.addPair(obj, p); err != nil {
			return nil, err
		}
	}
	return obj.m, nil
}

// setHas is SameValueZero membership for the values a set or map holds; objects are never equal to another object.
func setHas(values []any, v any) bool {
	for _, x := range values {
		switch a := x.(type) {
		case float64:
			if b, ok := v.(float64); ok && (a == b || (math.IsNaN(a) && math.IsNaN(b))) {
				return true
			}
		case string, bool, nil:
			if x == v {
				return true
			}
		}
	}
	return false
}

// jsTarget is the object or Map a pair is added to.
type jsTarget interface {
	has(key any) bool
	set(key any, value any)
}

type jsObject struct{ m map[string]any }

func (o *jsObject) has(key any) bool       { _, ok := o.m[propertyKey(key)]; return ok }
func (o *jsObject) set(key any, value any) { o.m[propertyKey(key)] = value }

type jsMapTarget struct{ m *Map }

func (t *jsMapTarget) has(key any) bool { return setHas(t.m.Keys, key) }
func (t *jsMapTarget) set(key any, value any) {
	for i, k := range t.m.Keys {
		if setHas([]any{k}, key) {
			t.m.Values[i] = value
			return
		}
	}
	t.m.Keys, t.m.Values = append(t.m.Keys, key), append(t.m.Values, value)
}

// propertyKey is the property name a JavaScript value takes as an object key.
func propertyKey(key any) string {
	switch k := key.(type) {
	case nil:
		return "null"
	case string:
		return k
	case bool:
		return strconv.FormatBool(k)
	case float64:
		return jsnumber.String(k)
	case Symbol:
		return "Symbol(" + string(k) + ")"
	case Date:
		return jsDateString(k.MS)
	case []byte:
		return bufferString(k)
	case Set:
		return "[object Set]"
	case Map:
		return "[object Map]"
	case []any:
		parts := make([]string, len(k))
		for i, x := range k {
			if _, isSymbol := x.(Symbol); isSymbol {
				throw("Cannot convert a Symbol value to a string")
			}
			if x != nil {
				parts[i] = propertyKey(x)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func (c *toJSCtx) addPair(target jsTarget, p *pair) error {
	if p.key.mergeKey {
		return c.addMerge(target, p.value)
	}
	jsKey, err := c.toJS(p.key)
	if err != nil {
		return err
	}
	if _, isObject := target.(*jsObject); !isObject {
		value, err := c.toJS(p.value)
		if err != nil {
			return err
		}
		target.set(jsKey, value)
		return nil
	}
	key, err := c.stringifyKey(p.key, jsKey)
	if err != nil {
		return err
	}
	value, err := c.toJS(p.value)
	if err != nil {
		return err
	}
	target.set(key, value)
	return nil
}

func (c *toJSCtx) resolveAliasValue(n *node) *node {
	if n != nil && n.kind == kindAlias {
		return c.resolve(n)
	}
	return n
}

func (c *toJSCtx) addMerge(target jsTarget, value *node) error {
	source := c.resolveAliasValue(value)
	if source != nil && source.kind == kindSeq && source.class == "seq" {
		for _, item := range source.items {
			if err := c.mergeValue(target, item); err != nil {
				return err
			}
		}
		return nil
	}
	return c.mergeValue(target, source)
}

func (c *toJSCtx) mergeValue(target jsTarget, value *node) error {
	source := c.resolveAliasValue(value)
	if source == nil || source.kind != kindMap || source.class != "map" {
		return errors.New("Merge sources must be maps or map aliases")
	}
	src := &Map{}
	st := &jsMapTarget{m: src}
	for _, p := range source.pairs {
		if err := c.addPair(st, p); err != nil {
			return err
		}
	}
	for i, key := range src.Keys {
		if !target.has(key) {
			target.set(key, src.Values[i])
		}
	}
	return nil
}

// stringifyKey is the property name a mapping key takes in the JavaScript object. A key whose value is an object is written with the library's stringifier.
func (c *toJSCtx) stringifyKey(key *node, jsKey any) (string, error) {
	switch k := jsKey.(type) {
	case nil:
		return "", nil
	case string:
		return k, nil
	case bool:
		return strconv.FormatBool(k), nil
	case float64:
		return jsnumber.String(k), nil
	case Symbol:
		return "Symbol(" + string(k) + ")", nil
	}
	if key.kind == kindScalar {
		switch k := jsKey.(type) {
		case Date:
			return jsDateString(k.MS), nil
		case []byte:
			return bufferString(k), nil
		}
	}
	ctx := &strCtx{anchors: map[string]bool{}, pushed: c.doc.pushed, inFlow: true}
	for n := range c.anchors {
		ctx.anchors[n.anchor] = true
	}
	return c.runStringify(ctx, key)
}

func (c *toJSCtx) runStringify(ctx *strCtx, key *node) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(stringifyError); ok {
				result, err = "", errors.New(se.message)
				return
			}
			panic(r)
		}
	}()
	if key.kind == kindMap || key.kind == kindSeq {
		// The library calls the collection's own toString, which prints no anchor or tag.
		return collectionString(ctx, c.doc, key), nil
	}
	return stringifyItem(ctx, c.doc, key, nil), nil
}

func (c *toJSCtx) nodesInOrder() []*node {
	if c.cached {
		return c.aliasNodes
	}
	var out []*node
	var visit func(n *node)
	visit = func(n *node) {
		if n == nil {
			return
		}
		if n.kind == kindAlias || n.hasAnchor() {
			out = append(out, n)
		}
		for _, item := range n.items {
			visit(item)
		}
		for _, p := range n.pairs {
			visit(p.key)
			visit(p.value)
		}
	}
	visit(c.doc.contents)
	c.aliasNodes, c.cached = out, true
	return out
}

func (c *toJSCtx) resolve(alias *node) *node {
	var found *node
	for _, n := range c.nodesInOrder() {
		if n == alias {
			break
		}
		if n.anchor == alias.source {
			found = n
		}
	}
	return found
}

func (c *toJSCtx) aliasCount(n *node) int {
	switch {
	case n == nil:
		return 1
	case n.kind == kindAlias:
		if source := c.resolve(n); source != nil {
			if a := c.anchors[source]; a != nil {
				return a.count * a.aliasCount
			}
		}
		return 0
	case n.kind == kindSeq || n.kind == kindMap:
		count := 0
		for _, item := range n.items {
			count = max(count, c.aliasCount(item))
		}
		for _, p := range n.pairs {
			count = max(count, c.aliasCount(p.key), c.aliasCount(p.value))
		}
		return count
	}
	return 1
}

func (c *toJSCtx) aliasToJSON(alias *node) (any, error) {
	source := c.resolve(alias)
	if source == nil {
		return nil, errors.New("Unresolved alias (the anchor must be set before the alias): " + alias.source)
	}
	data := c.anchors[source]
	if data == nil {
		if _, err := c.toJS(source); err != nil {
			return nil, err
		}
		data = c.anchors[source]
	}
	if data == nil || !data.done {
		return nil, errors.New("This should not happen: Alias anchor was not resolved?")
	}
	data.count++
	if data.aliasCount == 0 {
		data.aliasCount = c.aliasCount(source)
	}
	if data.count*data.aliasCount > maxAliasCount {
		return nil, errors.New("Excessive alias count indicates a resource exhaustion attack")
	}
	return data.res, nil
}

// Parse is parse(source) of the yaml package: the JavaScript value of the single document in source, or the error the library throws. A parse error message already carries the line, column and source excerpt.
func Parse(source string) (any, error) {
	tokens, lineStarts := parseCSTWithLines(source)
	c := newComposer()
	var docs []*document
	for _, token := range tokens {
		if yielded := c.next(token); yielded != nil {
			docs = append(docs, yielded)
		}
	}
	if last := c.end(true, len(source)); last != nil {
		docs = append(docs, last)
	}
	doc := docs[0]
	if len(docs) > 1 {
		doc.errors = append(doc.errors, &yamlError{code: "MULTIPLE_DOCS", pos: pos{docs[1].rng[0], docs[1].rng[1]}, message: "Source contains multiple documents; please use YAML.parseAllDocuments()"})
	}
	for _, e := range doc.errors {
		prettify(source, lineStarts, e)
	}
	if len(doc.errors) > 0 {
		return nil, errors.New(doc.errors[0].message)
	}
	ctx := &toJSCtx{anchors: map[*node]*anchorData{}, doc: doc}
	return ctx.toJSCatching(doc.contents)
}

// toJSCatching is toJS with the TypeErrors JavaScript throws while a key is converted turned into errors.
func (c *toJSCtx) toJSCatching(n *node) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(stringifyError); ok {
				result, err = nil, errors.New(se.message)
				return
			}
			panic(r)
		}
	}()
	return c.toJS(n)
}

// bufferString is String() of the Buffer the library's binary tag builds on Node (Buffer.from(src, "base64"), yaml 2.9.0 dist/schema/yaml-1.1/binary.js): its UTF-8 text as the WHATWG decoder reads it, one U+FFFD for each maximal invalid subsequence.
func bufferString(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		lo, hi := byte(0x80), byte(0xBF)
		var need int
		var cp rune
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need, cp = 1, rune(c&0x1F)
		case c >= 0xE0 && c <= 0xEF:
			need, cp = 2, rune(c&0x0F)
			switch c {
			case 0xE0:
				lo = 0xA0
			case 0xED:
				hi = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need, cp = 3, rune(c&0x07)
			switch c {
			case 0xF0:
				lo = 0x90
			case 0xF4:
				hi = 0x8F
			}
		default:
			sb.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j, ok := i+1, true
		for range need {
			if j >= len(b) || b[j] < lo || b[j] > hi {
				ok = false
				break
			}
			cp = cp<<6 | rune(b[j]&0x3F)
			j++
			lo, hi = 0x80, 0xBF
		}
		if !ok {
			// The byte that broke the sequence is read again as the start of the next one.
			sb.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		sb.WriteRune(cp)
		i = j
	}
	return sb.String()
}
