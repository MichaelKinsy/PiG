//go:build !goexperiment.jsonv2

package tools

// encodedInvalidUTF8 is what encoding/json writes for a byte of ill-formed UTF-8 in a string: the legacy encoder escapes U+FFFD.
const encodedInvalidUTF8 = `\ufffd`
