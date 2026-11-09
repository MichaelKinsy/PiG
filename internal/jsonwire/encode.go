package jsonwire

import (
	"encoding/json/jsontext"
	"sync"
)

// maxPooledText bounds the buffer WriteStringify keeps between calls (golang/go#23199).
const maxPooledText = 64 << 10

var textBuffers = sync.Pool{New: func() any { return new([]byte) }}

// WriteStringify writes the value whose JSON text appendTo appends as the next value of encoder. It is a MarshalJSONTo for a type that has an
// appender of its JSON text (a StringifyAppender): encoding/json then takes the text straight into the encoder's buffer, where a MarshalJSON
// result is a fresh slice that the encoder validates and copies, and the value is copied again to reach its method.
//
// The encoder applies its own options to the text it is given, so it escapes what its options escape.
func WriteStringify(encoder *jsontext.Encoder, appendTo func([]byte) ([]byte, error)) error {
	buffer := textBuffers.Get().(*[]byte)
	text, err := appendTo((*buffer)[:0])
	if err == nil {
		err = encoder.WriteValue(text)
	}
	if cap(text) <= maxPooledText {
		*buffer = text[:0]
		textBuffers.Put(buffer)
	}
	return err
}
