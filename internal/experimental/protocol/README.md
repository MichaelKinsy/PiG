# Experimental binary protocol

This package implements the pinned Pi protocol-v8 envelopes, strict CBOR subset, and four-byte big-endian framing. It does not start listeners, select a gateway, or enable experimental commands in the stable CLI.

## Values and validation

Use `Object` for ordered JavaScript own properties. It preserves insertion order and enumerates numeric index keys first. `__proto__` is ordinary data. Use `Undefined` to represent an omitted object property. Undefined array entries and symbol properties are rejected. Go `nil` represents JSON null. A `ResponseEnvelope` uses `HasResult` to distinguish a missing result from an explicit null result.

Envelope parsers accept decoded values, not JSON text. Use `FromJSON` and `ToJSON` only at the Go Chord `json.RawMessage` boundary. This conversion preserves numeric-key ordering, last-value duplicate keys, and lone UTF-16 units. CBOR subsequently rejects non-scalar strings rather than replacing them. An absent result stays absent; callers do not convert it to JSON null. The client/server message and routing target sets are closed Go unions. Payloads remain opaque strict JSON. The protocol layer does not interpret Chord service calls. It rejects non-finite numbers, byte strings inside JSON payloads, undefined properties, and cycles. The CBOR layer independently rejects unsafe integers, malformed UTF-8, tags, indefinite lengths, unsupported floating-point widths, duplicate map keys, and trailing data.

## Ownership and limits

Encoding and decoding are synchronous. Run transport work outside a TUI input or render loop. Each decoder belongs to one ordered connection reader.

The default frame and CBOR byte limit is 16 MiB. A frame decoder allocates 64 KiB payload blocks as bytes arrive instead of allocating the full declared length after reading a header. Returned frames and byte strings do not alias incoming chunks. Limit and truncation failures discard partial framing state and permanently fail the decoder. Ending an already ended decoder is an error.

The CBOR defaults permit one million container entries and depth 64. Caller options preserve explicit zero separately from an omitted value. No background tasks or timers are created by this package.

## Qualification

`framing_test.go`, `cbor_test.go`, and `messages_test.go` retain the corresponding pinned `packages/protocol/test/` cases, vectors, split points, limits, errors, and source citations. This lane runs package vet and lint only. Runtime qualification belongs to the merged test run; the package does not claim tested interoperability or performance measurements yet.
