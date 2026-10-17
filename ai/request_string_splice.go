package ai

import (
	"bytes"
	"encoding"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"sync"
)

// stringSplice lets any provider payload keep the encoding of its large strings across requests. rewrite copies the payload with each large string replaced by a token string; the provider marshals that small payload as it marshals the real one, and join puts each string's cached encoding where its token landed. The body is the bytes marshalRequestBody gives the real payload. pig additive (D112): see requestSpliceMinBytes.
type stringSplice struct {
	cache  *quotedStringCache
	nonce  string
	quoted [][]byte
	// tokenText is where the encoder's token is written.
	tokenText []byte
	// key and scratch are the message record and the encode buffer that tokenMessage reuses for each message.
	key     messageKey
	scratch bytes.Buffer
	// walking holds the pointers, maps and slices the walk is inside, so a payload that refers to itself is left as it is below the repeat. It lives in walkingBuf until a payload nests deeper, so an ordinary payload allocates nothing for it.
	walking    []walkedValue
	walkingBuf [walkingInline]walkedValue
}

// walkingInline is the nesting depth that needs no allocation; a deeper payload grows the slice.
const walkingInline = 16

// walkedValue identifies a pointer, map or slice by its address and type (and a slice by its length, which tells a slice from the longer one it starts).
type walkedValue struct {
	typ  reflect.Type
	data uintptr
	len  int
}

var (
	stringType     = reflect.TypeFor[string]()
	rawMessageType = reflect.TypeFor[json.RawMessage]()
)

// requestStringReadTag marks a field whose strings the provider reads when it marshals, which a token would change: the anthropic-beta header is built from the request's betas.
const requestStringReadTag = "requestsplice"

// requestStringCarrier marks a type with its own MarshalJSON that writes each string field as the JSON string of that field, unread and unchanged, so a token stands in for the string. A marshaler without the mark is left alone: its strings are encoded by it each time.
type requestStringCarrier interface{ carriesRequestStrings() }

// requestStringEncoder marks a payload type that writes every plain string in it as one JSON string, with no struct tag option (",string" or "format") that writes it in another form. The encoder can then write the token of a large string itself (stringSplice.encode) and the payload is not copied with the tokens in it. A payload without the mark is copied.
type requestStringEncoder interface{ encodesRequestStrings() }

var (
	marshalerTypes = []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[encoding.TextMarshaler]()}
	carrierType    = reflect.TypeFor[requestStringCarrier]()
	walkPlans      sync.Map // reflect.Type -> *walkPlan
)

// walkPlan is what the walk needs to know about a type: whether it can be left unvisited, and which fields of a struct it visits.
type walkPlan struct {
	// skip is set for a type that writes itself with a marshaler that does not carry its strings through, and for a type that holds no string the walk replaces, no raw message and no interface, however its values are filled.
	skip   bool
	opaque bool
	fields []int
}

// planOf is the walk plan of t, computed once per type.
func planOf(t reflect.Type) *walkPlan {
	if known, ok := walkPlans.Load(t); ok {
		return known.(*walkPlan)
	}
	plan := &walkPlan{opaque: opaque(t)}
	// A raw message is a marshaler, and the walk replaces it whole when the cache wrote it, so it is never skipped.
	plan.skip = plan.opaque && t != rawMessageType
	if t.Kind() == reflect.Struct && !plan.opaque {
		for i := range t.NumField() {
			field := t.Field(i)
			if field.IsExported() && field.Tag.Get(requestStringReadTag) == "" && !inert(field.Type) {
				plan.fields = append(plan.fields, i)
			}
		}
	}
	plan.skip = plan.skip || (t != rawMessageType && inert(t))
	walkPlans.Store(t, plan)
	return plan
}

// inert reports whether no value of t can hold what the walk replaces: a string of the plain string type, a raw message or an interface value, reached through the fields the walk visits.
func inert(t reflect.Type) bool {
	seen := map[reflect.Type]bool{}
	var reaches func(reflect.Type) bool
	reaches = func(t reflect.Type) bool {
		if seen[t] {
			return false
		}
		seen[t] = true
		if t == rawMessageType || t == stringType || t.Kind() == reflect.Interface {
			return true
		}
		if opaque(t) {
			return false
		}
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			if t.Kind() != reflect.Pointer && t.Elem().Kind() == reflect.Uint8 {
				return false
			}
			return reaches(t.Elem())
		case reflect.Map:
			return reaches(t.Elem())
		case reflect.Struct:
			for field := range t.Fields() {
				if field.IsExported() && field.Tag.Get(requestStringReadTag) == "" && reaches(field.Type) {
					return true
				}
			}
		}
		return false
	}
	return !reaches(t)
}

// opaque reports whether t writes itself with a marshaler that does not carry its strings through.
func opaque(t reflect.Type) bool {
	pointer := reflect.PointerTo(t)
	result := false
	for _, marshaler := range marshalerTypes {
		if t.Implements(marshaler) || pointer.Implements(marshaler) {
			result = !t.Implements(carrierType) && !pointer.Implements(carrierType)
		}
	}
	return result
}

// marshalSpliced is marshal(payload) with the encoding of each large string in the payload taken from cache; a nil cache takes none. marshal must write the payload as marshalRequestBody does and must carry a string through without reading it. A payload whose skeleton does not hold every token as a whole JSON string is marshalled as it is, so a marshaler that rewrites a string costs the cache and never changes the body.
func marshalSpliced(payload any, cache *quotedStringCache, marshal func(any) ([]byte, error)) (*requestBody, error) {
	if cache != nil {
		cache.beginRequest()
		if cache.messages != nil {
			cache.messages.beginRequest()
		}
	}
	splice := &stringSplice{cache: cache, nonce: newSpliceNonce()}
	if cache != nil {
		splice.quoted = make([][]byte, 0, cache.tokens.Load())
		defer func() { cache.tokens.Store(int64(len(splice.quoted))) }()
	}
	// The encoder that can write a token where it meets a large string does so without copying the payload; any other marshaler is given a copy of the payload that holds the tokens.
	skeleton, tokened, err := splice.encode(payload, marshal)
	if err != nil {
		return nil, err
	}
	if !tokened {
		var changed bool
		var rewritten any
		if rewritten, changed = splice.tokens(payload); changed {
			if skeleton, err = marshal(rewritten); err != nil {
				return nil, err
			}
		}
		tokened = changed
	}
	if tokened {
		if len(splice.quoted) == 0 {
			// The skeleton is the whole body.
			return &requestBody{segments: [][]byte{skeleton}, size: len(skeleton)}, nil
		}
		if body, ok := splice.join(skeleton); ok {
			return body, nil
		}
	}
	whole, err := marshal(payload)
	if err != nil {
		return nil, err
	}
	return &requestBody{segments: [][]byte{whole}, size: len(whole)}, nil
}

// tokens is payload with each large string replaced by its token, and whether any was.
func (s *stringSplice) tokens(payload any) (any, bool) {
	if s.cache == nil {
		return payload, false
	}
	rewritten, changed := s.rewrite(reflect.ValueOf(payload))
	if !changed {
		return payload, false
	}
	return rewritten.Interface(), true
}

// rewrite returns v with each large string replaced by its token and whether any was. Only a value that holds one is copied, along the path to it; the payload itself is never written to.
func (s *stringSplice) rewrite(v reflect.Value) (reflect.Value, bool) {
	// A payload that refers to itself through a pointer, map or slice is left as it is at the repeat, so the marshaler reports the cycle as it does for any payload instead of the walk exhausting the stack.
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if !v.IsNil() {
			key := walkedValue{typ: v.Type(), data: v.Pointer()}
			if v.Kind() == reflect.Slice {
				key.len = v.Len()
			}
			if slices.Contains(s.walking, key) {
				return v, false
			}
			if s.walking == nil {
				s.walking = s.walkingBuf[:0]
			}
			s.walking = append(s.walking, key)
			defer func() { s.walking = s.walking[:len(s.walking)-1] }()
		}
	}
	if v.Type() == rawMessageType {
		// A converter wrote this raw JSON string from the cache, so its token stands for it.
		raw := v.Bytes()
		if !s.cache.holds(raw) {
			return v, false
		}
		token := spliceToken(s.nonce, len(s.quoted))
		s.quoted = append(s.quoted, raw)
		return reflect.ValueOf(json.RawMessage(token)), true
	}
	var plan *walkPlan
	if v.Kind() != reflect.String {
		if plan = planOf(v.Type()); plan.skip && v.Kind() != reflect.Interface {
			return v, false
		}
	}
	switch v.Kind() {
	case reflect.String:
		if v.Type() != stringType || v.Len() < requestSpliceMinBytes {
			return v, false
		}
		quoted, ok := s.cache.quoteValid(v.String())
		if !ok {
			return v, false
		}
		token := spliceToken(s.nonce, len(s.quoted))
		s.quoted = append(s.quoted, quoted)
		return reflect.ValueOf(token[1 : len(token)-1]), true
	case reflect.Interface:
		if v.IsNil() {
			return v, false
		}
		inner, changed := s.rewrite(v.Elem())
		if !changed {
			return v, false
		}
		wrapped := reflect.New(v.Type()).Elem()
		wrapped.Set(inner)
		return wrapped, true
	case reflect.Pointer:
		if v.IsNil() {
			return v, false
		}
		inner, changed := s.rewrite(v.Elem())
		if !changed {
			return v, false
		}
		copied := reflect.New(v.Type().Elem())
		copied.Elem().Set(inner)
		return copied, true
	case reflect.Struct:
		var copied reflect.Value
		for _, i := range plan.fields {
			field, changed := s.rewrite(v.Field(i))
			if !changed {
				continue
			}
			if !copied.IsValid() {
				copied = reflect.New(v.Type()).Elem()
				copied.Set(v)
			}
			copied.Field(i).Set(field)
		}
		if !copied.IsValid() {
			return v, false
		}
		return copied, true
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() || v.Type().Elem().Kind() == reflect.Uint8 || v.Type().Elem().Kind() != reflect.Interface && planOf(v.Type().Elem()).skip {
			return v, false
		}
		var copied reflect.Value
		for i := range v.Len() {
			element, changed := s.rewrite(v.Index(i))
			if !changed {
				continue
			}
			if !copied.IsValid() {
				if v.Kind() == reflect.Slice {
					copied = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
				} else {
					copied = reflect.New(v.Type()).Elem()
				}
				reflect.Copy(copied, v)
			}
			copied.Index(i).Set(element)
		}
		if !copied.IsValid() {
			return v, false
		}
		return copied, true
	case reflect.Map:
		if v.IsNil() {
			return v, false
		}
		var copied reflect.Value
		for iterator := v.MapRange(); iterator.Next(); {
			element, changed := s.rewrite(iterator.Value())
			if !changed {
				continue
			}
			if !copied.IsValid() {
				copied = reflect.MakeMapWithSize(v.Type(), v.Len())
				for all := v.MapRange(); all.Next(); {
					copied.SetMapIndex(all.Key(), all.Value())
				}
			}
			copied.SetMapIndex(iterator.Key(), element)
		}
		if !copied.IsValid() {
			return v, false
		}
		return copied, true
	}
	return v, false
}

// join builds the body from the marshalled skeleton, or reports false when a token is not a whole JSON string of the skeleton.
func (s *stringSplice) join(skeleton []byte) (*requestBody, bool) {
	body := &requestBody{segments: make([][]byte, 0, 2*len(s.quoted)+1)}
	rest := skeleton
	nonce := []byte(s.nonce)
	for {
		at := bytes.Index(rest, nonce)
		if at < 0 {
			break
		}
		start := at - 1
		digits := at + len(s.nonce) + 1
		if start < 0 || rest[start] != '"' || digits > len(rest) || rest[digits-1] != '-' {
			return nil, false
		}
		end := digits
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		index, err := strconv.Atoi(string(rest[digits:end]))
		if err != nil || index >= len(s.quoted) || end >= len(rest) || rest[end] != '"' {
			return nil, false
		}
		body.add(rest[:start])
		body.add(s.quoted[index])
		rest = rest[end+1:]
	}
	body.add(rest)
	if body.size <= requestSpliceMinBytes && len(body.segments) > 1 {
		// A body of one segment goes out in the same write as the request headers (requestBody.reader); a short body of cached messages must too. Sent apart, a server that answers without reading the body closes on unread request bytes, and the client reads a connection reset where Pi's fetch reads the server's own end of the stream.
		whole := body.Bytes()
		body = &requestBody{segments: [][]byte{whole}, size: len(whole)}
	}
	return body, true
}
