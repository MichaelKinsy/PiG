// Ports Pi packages/durable/test/codemode-imports.test.ts.

// pi: packages/durable/src/codemode/codemode.ts

package codemode

import (
	"context"
	"testing"

	cm "github.com/MichaelKinsy/PiG/codemode"
)

// panicSandbox fails the test when the extension starts a script while it is created.
type panicSandbox struct{ t *testing.T }

func (s panicSandbox) Start(context.Context, string, cm.RunOptions) (*cm.Run, error) {
	s.t.Fatal("the Code Mode extension started a sandbox")
	return nil, nil
}

// Pi mocks quickjs-wasi to throw and asserts that loading the extension does not load pi-codemode's WASM runtime (#11254),
// because the extension runs scripts in the sandbox the host passes. Go has no module loading: the package embeds the QuickJS
// module and inflates it on first use (codemode.QuickJSWasm), and the Sandbox interface is the only way a script runs. The Go
// form of the test is that creating the extension builds the tool and the prompt section without touching the sandbox.
func TestTheCodeModeExtensionLoadsWithoutPiCodemodesWasmRuntime(t *testing.T) {
	extension := CreateExtension(ToolOptions{Sandbox: panicSandbox{t}})
	if extension == nil || extension.Name != "codemode" || len(extension.Tools) == 0 {
		t.Fatalf("CreateExtension = %+v, want the codemode extension with its tools", extension)
	}
}
