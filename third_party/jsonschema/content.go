package jsonschema

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
)

// Decoder specifies how to decode specific contentEncoding.
type Decoder struct {
	// Name of contentEncoding.
	Name string
	// Decode given string to byte array.
	Decode func(string) ([]byte, error)
}

// base64Decoder is the one built-in decoder. PiG fork: v6.0.2 held the built-ins in maps built by a package init(); a static value and a lookup by name allocate nothing at start.
var base64Decoder = Decoder{
	Name: "base64",
	Decode: func(s string) ([]byte, error) {
		return base64.StdEncoding.DecodeString(s)
	},
}

// builtinDecoder returns the built-in decoder of the contentEncoding name, or nil.
func builtinDecoder(name string) *Decoder {
	if name == base64Decoder.Name {
		return &base64Decoder
	}
	return nil
}

// MediaType specified how to validate bytes against specific contentMediaType.
type MediaType struct {
	// Name of contentMediaType.
	Name string

	// Validate checks whether bytes conform to this mediatype.
	Validate func([]byte) error

	// UnmarshalJSON unmarshals bytes into json value.
	// This must be nil if this mediatype is not compatible
	// with json.
	UnmarshalJSON func([]byte) (any, error)
}

// jsonMediaType is the one built-in media type.
var jsonMediaType = MediaType{
	Name: "application/json",
	Validate: func(b []byte) error {
		var v any
		return json.Unmarshal(b, &v)
	},
	UnmarshalJSON: func(b []byte) (any, error) {
		return UnmarshalJSON(bytes.NewReader(b))
	},
}

// builtinMediaType returns the built-in media type of the contentMediaType name, or nil.
func builtinMediaType(name string) *MediaType {
	if name == jsonMediaType.Name {
		return &jsonMediaType
	}
	return nil
}
