//go:build !tinygo

package codemode

import (
	_ "embed"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

// Ports packages/codemode/src/wasm.ts (the quickjs.wasm module bytes that loadQuickJSWasm compiles)

// The binary carries the module as one zstd frame (260 KB, not 637 KB); assets/quickjs.wasm is the same bytes uncompressed, the file the oracle tests and the refresh steps in assets/PROVENANCE.md read.
//
//go:generate go run ../automation/gen/zstdfile assets/quickjs.wasm assets/quickjs.wasm.zst
//go:embed assets/quickjs.wasm.zst
var quickJSWasmFrame []byte

// quickJSWasm decodes the embedded module once, on first use.
var quickJSWasm = sync.OnceValue(func() []byte {
	module, err := zstdblob.Decode(quickJSWasmFrame)
	if err != nil {
		panic("codemode: embedded quickjs.wasm.zst: " + err.Error())
	}
	return module
})

// QuickJSWasm returns the embedded quickjs-wasi module (assets/PROVENANCE.md). The caller must not modify it.
func QuickJSWasm() []byte { return quickJSWasm() }
