//go:build go1.27 && goexperiment.jsonv2

package ai

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"hash/maphash"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// requestMessageSplicer marks a payload whose messages the encoder takes from the message cache: spliceMessages is the marshaler that writes the token of a message in its place. The payload must also be a requestStringEncoder, and a message must write each of its strings as the JSON string of that string, so that a message encoded on its own is the bytes it is in the request.
type requestMessageSplicer interface {
	spliceMessages(s *stringSplice) *jsonv2.Marshalers
}

// spliceMessages is the marshaler of messages of type M, whose record is keyOf.
func spliceMessages[M any](s *stringSplice, keyOf func(*messageKey, *M) bool) *jsonv2.Marshalers {
	// One copy of the message serves every message of the request: the record and the encoding are made before the marshaler returns, and a pointer to a local would be allocated for each message.
	held := new(M)
	return jsonv2.MarshalToFunc(func(encoder *jsontext.Encoder, message M) error {
		*held = message
		return tokenMessage(s, encoder, held, keyOf)
	})
}

// tokenMessage writes the token of a message whose encoding the cache holds, or has just encoded. A message it cannot record or encode is left to the encoder, which writes it, and its error if it has one, as it would without the cache.
func tokenMessage[M any](s *stringSplice, encoder *jsontext.Encoder, message *M, keyOf func(*messageKey, *M) bool) error {
	s.key.reset()
	if !keyOf(&s.key, message) {
		return errors.ErrUnsupported
	}
	cache := s.cache.messages
	hash := maphash.Bytes(cache.seed, s.key.buf)
	encoded := cache.lookup(hash, s.key.buf)
	if encoded == nil {
		s.scratch.Reset()
		if err := jsonv2.MarshalWrite(&s.scratch, message, requestBodyOptions); err != nil || bytes.Contains(s.scratch.Bytes(), replacementCharacter) {
			// A lone UTF-16 surrogate (or an error) is for the whole body to deal with, as marshalRequestBody does.
			return errors.ErrUnsupported
		}
		encoded = bytes.Clone(jsstring.UnescapeForStringifyInPlace(s.scratch.Bytes()))
		cache.store(hash, s.key.buf, encoded)
	}
	return s.writeToken(encoder, encoded)
}
