// Package cloudflare is the Cloudflare entry of codemode: the remote end of a remote sandbox as a Durable Object, and the inline
// sandbox with the QuickJS module the host bundled.
//
// Ports packages/codemode/src/cloudflare.ts. Pi's entry imports `cloudflare:workers`, which exists only inside a Worker; here the
// Go API is the same behavior for a Go host (QuickJS on wazero), and the Worker itself is the TinyGo build of package codemode
// (cmd/codemodewasm) served by durable/core/host/cf/src/codemode/sandbox-object.ts, whose limits are the ones of
// SandboxDurableObject.
package cloudflare

import (
	"context"

	"github.com/MichaelKinsy/PiG/codemode"
)

// DefaultRemoteMemoryLimitBytes is the default VM memory limit of SandboxDurableObject. Isolates have 128 MB in total.
//
// Ports packages/codemode/src/cloudflare.ts (DEFAULT_REMOTE_MEMORY_LIMIT_BYTES).
const DefaultRemoteMemoryLimitBytes uint32 = 32 * 1024 * 1024

// QuickJSWasm is the QuickJS module of this entry: the one the package embeds, which Wrangler's CompiledWasm rule supplies
// to a Worker. The caller must not modify it.
//
// Ports packages/codemode/src/cloudflare.ts (cloudflareQuickJSWasm).
func QuickJSWasm() []byte { return codemode.QuickJSWasm() }

// CreateInlineSandbox is InlineSandbox with this entry's QuickJS module: the scripts run on the caller's goroutine, which a
// script that computes blocks until it awaits a tool or uses up its interrupt budget.
//
// Ports packages/codemode/src/cloudflare.ts (createInlineCodemodeSandbox). options.Wasm is ignored: the module is QuickJSWasm.
func CreateInlineSandbox(options codemode.InlineSandboxOptions) (*codemode.InlineSandbox, error) {
	options.Wasm = func() ([]byte, error) { return QuickJSWasm(), nil }
	return codemode.NewInlineSandbox(options)
}

// SandboxDurableObject is the remote end of a remote sandbox: Execute runs a script's VM and calls back into the client for every
// tool call. The object keeps no state between executions. Limits apply to every request, whatever the client asks for: a
// request may ask for less, never more.
//
// Ports packages/codemode/src/cloudflare.ts (CodemodeSandboxDurableObject).
type SandboxDurableObject struct {
	// MaxInterruptBudget is the interrupt polls per execution. Zero means codemode.DefaultInterruptBudget.
	MaxInterruptBudget int
	// MaxMemoryLimitBytes is the VM memory per execution. Zero means DefaultRemoteMemoryLimitBytes.
	MaxMemoryLimitBytes uint32
}

// NewSandboxDurableObject returns an object with the default limits.
func NewSandboxDurableObject() *SandboxDurableObject {
	return &SandboxDurableObject{MaxInterruptBudget: codemode.DefaultInterruptBudget, MaxMemoryLimitBytes: DefaultRemoteMemoryLimitBytes}
}

// Execute is called by a remote sandbox's client. It returns when the script has settled or was stopped, and never fails: what
// goes wrong is reported to the client through exchange.
//
// Ports packages/codemode/src/cloudflare.ts (CodemodeSandboxDurableObject.execute).
func (o *SandboxDurableObject) Execute(ctx context.Context, request codemode.RemoteRequest, exchange codemode.RemoteExchange) error {
	budget, memory := o.MaxInterruptBudget, o.MaxMemoryLimitBytes
	if budget == 0 {
		budget = codemode.DefaultInterruptBudget
	}
	if memory == 0 {
		memory = DefaultRemoteMemoryLimitBytes
	}
	codemode.ServeRemote(ctx, request, exchange, codemode.ServeOptions{MaxInterruptBudget: budget, MaxMemoryLimitBytes: memory})
	return nil
}
