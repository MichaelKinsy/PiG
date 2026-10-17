//go:build !tinygo

package codemode

import "fmt"

// EmbeddedQuickJSWasmPath names the one QuickJS module the package has: the embedded asset.
const EmbeddedQuickJSWasmPath = "assets/quickjs.wasm"

// LoadQuickJSWasm returns the QuickJS wasm module. Pi's `loadQuickJSWasm(path?)` reads the module at path, defaulting to the file of the installed quickjs-wasi package; PiG has one module source, the embedded asset (assets/PROVENANCE.md), and never reads a module from the file system. An empty path or EmbeddedQuickJSWasmPath is that module; any other path is an error. Pass the result through SandboxOptions.Wasm: sandboxes that load one module share one compilation.
//
// Ports packages/codemode/src/wasm.ts loadQuickJSWasm(path?).
func LoadQuickJSWasm(path string) ([]byte, error) {
	if path != "" && path != EmbeddedQuickJSWasmPath {
		return nil, fmt.Errorf("codemode: load quickjs wasm: %q is not the embedded module %q; codemode has no other module source", path, EmbeddedQuickJSWasmPath)
	}
	return QuickJSWasm(), nil
}
