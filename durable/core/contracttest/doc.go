// Package contracttest is the Go side of the Durable core conformance gate (docs/plan/durable-core/CONTRACT.md sections 5-7).
//
// The Node tooling in durable/contract captures pi-durable and compares implementations at the store, commit, context and
// output levels. This package supplies what a Go core needs from that gate:
//
//   - the byte-level differential vectors of CONTRACT 5.6, generated from the reference's own code
//     (durable/contract/tools/gen-oracle.mjs) and embedded here: encoder, scanner, Chord ops, tool-argument validation and
//     uuidv7. The Check* functions run one vector family against a function the core under test supplies, so this package
//     imports nothing from the core and the core's tests can import this package;
//   - ReferenceEncode, ReferenceIndex and the fuzz kit (AddEncodeSeeds, EncodeFuzz, AddScanSeeds, ScanFuzz): Go models of the
//     reference's JSON.stringify(JSON.parse(x)) and entry-record extraction, proved against the vectors, so a core fuzzes its
//     encoder and scanner against an oracle with unlimited inputs;
//   - Writer and ReadTrace: the trace format (JSON Lines, CONTRACT 7.6) of a native run, statements only, and the crash switch
//     (CONTRACT_CRASH_AFTER) the Node gate sets; cmd/replayhost is a script-driven host that proves the native path.
//
// Fuzz runs are bounded: GOMAXPROCS=8 go test -fuzz=<name> -parallel=4 -fuzztime=30s.
package contracttest
