# Go extension JSON codec

This package is the shared host/Go-SDK JSON codec. It preserves the native `internal/jsstring` WTF-8 representation of unmatched UTF-16 units. It writes those units as standard JSON surrogate escapes and decodes those escapes back to WTF-8. Ordinary paired characters remain UTF-8. Other malformed UTF-8 retains Go's replacement behavior.

A post-processing wrapper around `encoding/json.Marshal` or `Unmarshal` cannot recover replaced units. Reflective projection would duplicate struct-field selection, custom marshalers, omission, cycle and map-key rules. This package instead retains Go's codec and changes the two string primitives. Use it at payload construction and decoding, not only at final envelope framing. Already-corrupted `json.RawMessage` data cannot be repaired.

## Provenance

The production files derive from the Go 1.27.1 `src/encoding/json` v1 implementation: `decode.go`, `encode.go`, `fold.go`, `indent.go`, `scanner.go`, `stream.go`, `tables.go`, and `tags.go`. The Go Authors' BSD license is retained in `LICENSE` and each source header. Source: <https://github.com/golang/go/tree/go1.27.1/src/encoding/json>.

Local changes are the surrogate branches in `appendString` and `unquoteBytes`, string-contract comments, removal of the standard library's internal experiment build tags, aliases for `encoding/json.RawMessage` and `encoding/json.Number`, a pool for `Unmarshal`'s decode state, scanner stack and error context (`decodeStatePool`, so a small message decodes without allocating them), overflow-safe allocation sizes (`addSize`, `mulSize` in `allocsize.go`) where `unquoteBytes` and `Grow` size a buffer, and narrow lint fixes or explanations, and `quoteChar`, which is `strconv.QuoteRune` in place of the standard library's hand-written quoting. The experiment tags belong to Go's selection of its own implementation; this package always uses these sources. The aliases preserve interoperability with callers of the standard library.

`UnmarshalFrame` takes a document nested 64 levels past the standard library's 10000: a frame the host sends holds arguments nested to 10000 levels inside an envelope.

Other local changes (lw-json):

- The JSON.stringify mode: `MarshalStringify`/`AppendStringify`, `encOpts.jsStrings` and `appendCompactJS` for nested Marshaler output, plus the `StringifyAppender` splice.
- `UnmarshalTrusted`.
- An eight-byte scan in `appendString`.
- A plain-slice `encodeState` buffer in place of the embedded `bytes.Buffer`, with pooled buffers capped at 64 KiB.
- The verify mode (`verify_off.go`/`verify_on.go`, build tag `pigjson_verify`).
- `Unmarshal` checks validity first with `validFast` (`fastvalid.go`, a table-driven single pass that is a copy of `internal/jsonvalid`) and asks the scanner's `checkValid` only for text the fast pass rejects, which words the error; `decodeState.skip` finds the end of a container by counting brackets and passing strings whole (`skipContainer`) instead of stepping the scanner per byte, and leaves the scanner as the per-byte steps would. Both are held to the scanner by differential tests (`fastvalid_test.go`).

When updating the toolchain, compare these files with that exact Go source tree and retain the string regression suite. Do not replace the codec with placeholder characters or a second wire shape. SDK staging includes this subpackage and its license.
