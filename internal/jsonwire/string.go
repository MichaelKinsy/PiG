// Package jsonwire holds the shared pieces of the wire decoders and encoders that sit on encoding/json.
package jsonwire

import (
	"encoding/json"
	"unsafe"
)

// Bytes is the bytes of s without a copy. The caller must not write to the result or keep it past the call that was given it: it is the memory of
// a string, which is immutable. It is for passing a string to a function that only reads its bytes (json.Valid, json.Unmarshal, which copy what
// they keep). In Go 1.27 encoding/json's engine lets the slice it is given escape, so []byte(s) at such a call allocates a copy of every
// event data line that the Go 1.26 engine checked in place.
func Bytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s)) //nolint:gosec // G103: read-only view of an immutable string for a callee that neither writes nor retains it
}

// Valid is json.Valid(s) without the copy of s that json.Valid([]byte(s)) makes.
func Valid(s string) bool { return json.Valid(Bytes(s)) }

// Unmarshal is json.Unmarshal([]byte(s), v) without the copy of s. encoding/json copies the strings and raw messages it stores, so v holds no
// reference to s; v must not be, or hold, a type whose UnmarshalJSON keeps the slice it is given, which this would hand the memory of s.
func Unmarshal(s string, v any) error { return json.Unmarshal(Bytes(s), v) }
