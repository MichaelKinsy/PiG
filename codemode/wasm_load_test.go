package codemode

import (
	"bytes"
	"os"
	"testing"
)

// upstream: packages/codemode/src/wasm.ts:21 loadQuickJSWasm(path?): the default is the installed module. PiG has one module source, the embedded asset, so the path must name it.
func TestLoadQuickJSWasmReturnsOnlyTheEmbeddedModule(t *testing.T) {
	for _, path := range []string{"", EmbeddedQuickJSWasmPath} {
		got, err := LoadQuickJSWasm(path)
		if err != nil || !bytes.Equal(got, QuickJSWasm()) {
			t.Fatalf("LoadQuickJSWasm(%q) = %d bytes, %v; want the embedded module", path, len(got), err)
		}
	}
	if got, err := LoadQuickJSWasm("/some/other/quickjs.wasm"); err == nil || got != nil {
		t.Fatalf("a path other than the embedded module loaded: %d bytes, %v", len(got), err)
	}
}

// The binary embeds assets/quickjs.wasm.gz; its inflated bytes are the module in assets/quickjs.wasm, which the oracle tests and the provenance hash read.
func TestEmbeddedQuickJSWasmGzipInflatesToTheAsset(t *testing.T) {
	want, err := os.ReadFile(EmbeddedQuickJSWasmPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(QuickJSWasm(), want) {
		t.Fatalf("inflated embedded module is %d bytes, assets/quickjs.wasm is %d: run go generate ./codemode", len(QuickJSWasm()), len(want))
	}
}
