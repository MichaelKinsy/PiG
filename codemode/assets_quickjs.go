//go:build !tinygo

package codemode

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"io"
	"sync"
)

// The binary carries the module gzip-compressed (287 KB, not 637 KB); assets/quickjs.wasm is the same bytes uncompressed, the file the oracle tests and the refresh steps in assets/PROVENANCE.md read.
//
//go:generate sh -c "gzip -9nc assets/quickjs.wasm > assets/quickjs.wasm.gz"
//go:embed assets/quickjs.wasm.gz
var quickJSWasmGzip []byte

// quickJSWasm inflates the embedded module once, on first use. The gzip trailer's last four bytes are the inflated size.
var quickJSWasm = sync.OnceValue(func() []byte {
	reader, err := gzip.NewReader(bytes.NewReader(quickJSWasmGzip))
	if err != nil {
		panic("codemode: embedded quickjs.wasm.gz: " + err.Error())
	}
	module := make([]byte, binary.LittleEndian.Uint32(quickJSWasmGzip[len(quickJSWasmGzip)-4:]))
	if _, err := io.ReadFull(reader, module); err != nil {
		panic("codemode: embedded quickjs.wasm.gz: " + err.Error())
	}
	return module
})

// QuickJSWasm returns the embedded quickjs-wasi module (assets/PROVENANCE.md). The caller must not modify it.
func QuickJSWasm() []byte { return quickJSWasm() }
