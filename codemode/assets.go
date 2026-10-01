package codemode

import (
	_ "embed"
)

// PreludeSource is the JavaScript evaluated inside the VM before the script runs, embedded byte for byte.
//
// Ports packages/codemode/src/runtime/prelude-source.ts (PRELUDE_SOURCE).
//
//go:embed assets/prelude.js
var PreludeSource string

//go:embed assets/quickjs.wasm
var quickJSWasm []byte

// QuickJSWasm returns the embedded quickjs-wasi module (assets/PROVENANCE.md). The caller must not modify it.
func QuickJSWasm() []byte { return quickJSWasm }

// The store limits the prelude enforces.
//
// Ports packages/codemode/src/runtime/prelude-source.ts (MAX_STORE_VALUE_CHARS, MAX_STORE_TOTAL_CHARS).
const (
	MaxStoreValueChars = 256 * 1024
	MaxStoreTotalChars = 1024 * 1024
)
