//go:build go1.27 && goexperiment.jsonv2

package ai

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// requestBodyOptions are encoding/json's own options without its two JavaScript-unsafe escapes, which JSON.stringify does not make.
var requestBodyOptions = jsonv2.JoinOptions(json.DefaultOptionsV1(), jsontext.EscapeForHTML(false), jsontext.EscapeForJS(false))

// maxKeptRequestScratch bounds the encode buffer kept between requests: a larger one is dropped with the request that grew it.
// pig additive (D112): Pi keeps no encode buffer between JSON.stringify calls; a dropped buffer is made again and encodes the same bytes.
const maxKeptRequestScratch = 4 << 20

// requestScratch is the buffer requests are encoded into. The encoder writes straight into a *bytes.Buffer, and jsonv2.Marshal's own buffer
// starts small and doubles, so a long history's skeleton paid for every doubling and then a copy out; this one has the size of the largest recent
// request, and a body is one exact copy of what is written into it. It is kept between requests, not pooled: a pool is emptied by the garbage
// collector, which in a long session comes between requests, and then every build would grow a buffer again.
var requestScratch struct {
	mu   sync.Mutex
	kept *bytes.Buffer
}

// takeScratch returns the kept buffer, emptied, or a new one: a request that encodes while another does, or inside its marshaler, takes its own.
func takeScratch() *bytes.Buffer {
	requestScratch.mu.Lock()
	scratch := requestScratch.kept
	requestScratch.kept = nil
	requestScratch.mu.Unlock()
	if scratch == nil {
		return new(bytes.Buffer)
	}
	scratch.Reset()
	return scratch
}

// keepScratch keeps the larger of scratch and the buffer already kept, unless it is too large to keep.
func keepScratch(scratch *bytes.Buffer) {
	if scratch.Cap() > maxKeptRequestScratch {
		return
	}
	requestScratch.mu.Lock()
	if requestScratch.kept == nil || requestScratch.kept.Cap() < scratch.Cap() {
		requestScratch.kept = scratch
	}
	requestScratch.mu.Unlock()
}

// marshalRequestBody is JSON.stringify(body), which Pi's provider SDKs send (openai 7.19.0 internal/request-options.js:29): '<', '>', '&',
// U+2028 and U+2029 as they are. encoding/json.Marshal escapes all five, and a nested MarshalJSON that calls it brings its escapes along,
// so those are written back.
func marshalRequestBody(body any) ([]byte, error) {
	if canonical, raw, err := rawRequestBody(body); raw {
		return canonical, err
	}
	return encodeRequestBody(body, requestBodyOptions)
}

// encodeRequestBody is marshalRequestBody of a body that is not raw JSON, written under options.
func encodeRequestBody(body any, options jsonv2.Options) ([]byte, error) {
	scratch := takeScratch()
	defer keepScratch(scratch)
	if err := jsonv2.MarshalWrite(scratch, body, options); err != nil {
		return nil, err
	}
	encoded := scratch.Bytes()
	if bytes.Contains(encoded, replacementCharacter) {
		// A lone UTF-16 surrogate: JSON.stringify writes its escape, which jsonv2 writes as U+FFFD for each byte of the WTF-8 form.
		return jsstring.MarshalJSON(body)
	}
	return bytes.Clone(jsstring.UnescapeForStringifyInPlace(encoded)), nil
}

// encode is marshalRequestBody of payload with each large string the cache holds written as its token, for a marshal that is marshalRequestBody; it reports false, having done nothing, for any other. The encoder hands each string of the payload to a function that writes the token, so the payload is not copied with the tokens in it as stringSplice.tokens copies it. A string the function leaves, because it is small or is not UTF-8, is encoded as it would be.
func (s *stringSplice) encode(payload any, marshal func(any) ([]byte, error)) ([]byte, bool, error) {
	if s.cache == nil || reflect.ValueOf(marshal).Pointer() != reflect.ValueOf(marshalRequestBody).Pointer() {
		return nil, false, nil
	}
	if _, ok := payload.(requestStringEncoder); !ok {
		return nil, false, nil
	}
	tokens := jsonv2.JoinMarshalers(jsonv2.MarshalToFunc(s.tokenString), jsonv2.MarshalToFunc(s.tokenRaw))
	if messages, ok := payload.(requestMessageSplicer); ok && s.cache.messages != nil {
		tokens = jsonv2.JoinMarshalers(messages.spliceMessages(s), tokens)
	}
	skeleton, err := encodeRequestBody(payload, jsonv2.JoinOptions(requestBodyOptions, jsonv2.WithMarshalers(tokens)))
	return skeleton, true, err
}

// tokenString writes the token of a large string the cache can encode.
func (s *stringSplice) tokenString(encoder *jsontext.Encoder, text string) error {
	// A key of a map is not a value of the payload: the encoder sorts the members by the name it wrote, which a token would change.
	if kind, length := encoder.StackIndex(encoder.StackDepth()); len(text) < requestSpliceMinBytes || kind == '{' && length%2 == 0 {
		return errors.ErrUnsupported
	}
	quoted, ok := s.cache.quoteValid(text)
	if !ok {
		return errors.ErrUnsupported
	}
	return s.writeToken(encoder, quoted)
}

// tokenRaw writes the token of raw JSON the cache wrote: a converter put it in the payload, and its token stands for it.
func (s *stringSplice) tokenRaw(encoder *jsontext.Encoder, raw json.RawMessage) error {
	if !s.cache.holds(raw) {
		return errors.ErrUnsupported
	}
	return s.writeToken(encoder, raw)
}

func (s *stringSplice) writeToken(encoder *jsontext.Encoder, quoted []byte) error {
	// The encoder copies the token into its own buffer, so the text of one is not allocated.
	token := strconv.AppendInt(append(append(s.tokenText[:0], s.nonce...), '-'), int64(len(s.quoted)), 10)
	s.tokenText = token[:0]
	s.quoted = append(s.quoted, quoted)
	return encoder.WriteToken(jsontext.String(unsafe.String(unsafe.SliceData(token), len(token)))) //nolint:gosec // G103: WriteToken copies the token into the encoder before returning, so the scratch buffer is not retained
}
