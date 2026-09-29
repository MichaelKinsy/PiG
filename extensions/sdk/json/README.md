# Go extension JSON codec

This package is the shared host/Go-SDK JSON codec. It preserves the native `internal/jsstring` WTF-8 representation of unmatched UTF-16 units. It writes those units as standard JSON surrogate escapes and decodes those escapes back to WTF-8. Ordinary paired characters remain UTF-8. Other malformed UTF-8 retains Go's replacement behavior.

A post-processing wrapper around `encoding/json.Marshal` or `Unmarshal` cannot recover replaced units. Reflective projection would duplicate struct-field selection, custom marshalers, omission, cycle and map-key rules. This package instead retains Go's codec and changes the two string primitives. Use it at payload construction and decoding, not only at final envelope framing. Already-corrupted `json.RawMessage` data cannot be repaired.

## Provenance

The production files derive from the Go 1.27.1 `src/encoding/json` v1 implementation: `decode.go`, `encode.go`, `fold.go`, `indent.go`, `scanner.go`, `stream.go`, `tables.go`, and `tags.go`. The Go Authors' BSD license is retained in `LICENSE` and each source header. Source: <https://github.com/golang/go/tree/go1.27.1/src/encoding/json>.

Local changes are the surrogate branches in `appendString` and `unquoteBytes`, string-contract comments, removal of the standard library's internal experiment build tags, aliases for `encoding/json.RawMessage` and `encoding/json.Number`, and narrow lint fixes or explanations. The experiment tags belong to Go's selection of its own implementation; this package always uses these sources. The aliases preserve interoperability with callers of the standard library.

When updating the toolchain, compare these files with that exact Go source tree and retain the string regression suite. Do not replace the codec with placeholder characters or a second wire shape. SDK staging includes this subpackage and its license.
