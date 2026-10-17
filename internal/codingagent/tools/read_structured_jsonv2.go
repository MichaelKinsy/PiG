//go:build goexperiment.jsonv2

package tools

// encodedInvalidUTF8 is what encoding/json writes for a byte of ill-formed UTF-8 in a string: built on encoding/json/v2, it writes U+FFFD
// as it is.
const encodedInvalidUTF8 = "\ufffd"
