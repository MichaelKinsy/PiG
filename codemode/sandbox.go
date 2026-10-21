//go:build !tinygo

package codemode

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// defaultTimeoutMs is the default deadline per execution.
//
// Ports packages/codemode/src/runtime/host.ts (DEFAULT_TIMEOUT_MS).
const defaultTimeoutMs = 300_000

// reservedGlobals are the names a global may not shadow.
//
// Ports packages/codemode/src/runtime/host.ts (RESERVED_GLOBALS).
var reservedGlobals = []string{"tools", "ALL_TOOLS", "console", "text", "image", "exit", "globalThis", "store", "load"}

// Sandbox runs scripts in QuickJS VMs. Each Execute gets its own VM; the sandbox holds only the tool table and
// defaults. Close aborts in-flight executions.
//
// Ports packages/codemode/src/runtime/host.ts (CodemodeSandbox).
type Sandbox struct {
	options SandboxOptions

	mu      sync.Mutex
	tools   []Tool
	toolSet map[string]struct{}
	globals []Tool
	closed  bool
	running map[*execution]struct{}

	// launcher starts the VM of each execution: the in-process worker of NewSandbox.
	launcher vmLauncher

	// prelude is the source evaluated before each script; tests replace it to send the host payloads the real prelude
	// never produces, as upstream's host tests replace the worker.
	prelude string

	// engines loads the compiled module for the sandbox's VMs.
	engines *engineSource
}

// NewSandbox validates the globals and registers the tools.
func NewSandbox(options SandboxOptions) (*Sandbox, error) {
	return newSandbox(options, func(s *Sandbox) vmLauncher { return s.launchWorker })
}

// newSandbox is NewSandbox for a sandbox whose VMs start through the launcher that launcher builds: the shared host of
// Pi's CodemodeSandboxBase, which each sandbox type completes with its own VmLauncher.
//
// Ports packages/codemode/src/runtime/execution.ts (CodemodeSandboxBase).
func newSandbox(options SandboxOptions, launcher func(*Sandbox) vmLauncher) (*Sandbox, error) {
	s := &Sandbox{options: options, running: map[*execution]struct{}{}, toolSet: make(map[string]struct{}, len(options.Tools)), prelude: PreludeSource, engines: &engineSource{wasm: options.Wasm, cacheDir: options.CacheDir}}
	s.launcher = launcher(s)
	if s.options.TimeoutMs == 0 {
		s.options.TimeoutMs = defaultTimeoutMs
	}
	for _, tool := range options.Tools {
		if err := s.RegisterTool(tool); err != nil {
			return nil, err
		}
	}
	names := make([]string, len(options.Globals))
	for i, global := range options.Globals {
		names[i] = global.Name
	}
	if err := checkGlobals(names); err != nil {
		return nil, err
	}
	s.globals = append(s.globals, options.Globals...)
	return s, nil
}

// checkGlobals checks global names: identifiers or `namespace.member`, not shadowing a built-in global or each other.
//
// Ports packages/codemode/src/runtime/execution.ts (checkGlobals).
func checkGlobals(globals []string) error {
	names := map[string]bool{}
	namespaces := map[string]bool{}
	var namespaceOrder []string
	for _, name := range globals {
		parts := strings.Split(name, ".")
		valid := len(parts) <= 2
		for _, part := range parts {
			valid = valid && isIdentifier(part)
		}
		if !valid || containsString(reservedGlobals, parts[0]) {
			return fmt.Errorf("Invalid global name %q", name)
		}
		if names[name] {
			return fmt.Errorf("Global %q is already registered", name)
		}
		if len(parts) == 2 && !namespaces[parts[0]] {
			namespaces[parts[0]] = true
			namespaceOrder = append(namespaceOrder, parts[0])
		}
		names[name] = true
	}
	for _, name := range namespaceOrder {
		if names[name] {
			return fmt.Errorf("Global %q conflicts with the namespace %q", name, name)
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	return slices.Contains(list, s)
}

// RegisterTool fails if a tool with the same name is already registered.
func (s *Sandbox) RegisterTool(tool Tool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.toolSet[tool.Name]; exists {
		return fmt.Errorf("Tool %q is already registered", tool.Name)
	}
	s.toolSet[tool.Name] = struct{}{}
	s.tools = append(s.tools, tool)
	return nil
}

// UnregisterTool removes a tool and reports whether it existed.
func (s *Sandbox) UnregisterTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, tool := range s.tools {
		if tool.Name == name {
			delete(s.toolSet, name)
			s.tools = append(s.tools[:i:i], s.tools[i+1:]...)
			return true
		}
	}
	return false
}

// Tools lists the registered tools in registration order.
func (s *Sandbox) Tools() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Tool(nil), s.tools...)
}

// Globals lists the registered globals in registration order.
func (s *Sandbox) Globals() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Tool(nil), s.globals...)
}

var errSandboxClosed = errors.New("Sandbox is closed")

// Execute runs code as an async function body: `return` and top-level `await` work. Script failures come back as a
// Result with OK false, never as an error; the error is for a closed sandbox. Cancelling ctx aborts the execution:
// the abort message is the context's cause. Execute returns after the execution's VM is closed and every goroutine
// it started has exited.
func (s *Sandbox) Execute(ctx context.Context, code string, options ExecuteOptions) (Result, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Result{}, errSandboxClosed
	}
	timeoutMs := s.options.TimeoutMs
	if options.TimeoutMs != 0 {
		timeoutMs = options.TimeoutMs
	}
	e := newExecution(s, code, timeoutMs, options.Store, append([]Tool(nil), s.tools...), append([]Tool(nil), s.globals...))
	s.running[e] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, e)
		s.mu.Unlock()
	}()
	return e.run(ctx), nil
}

// Close aborts in-flight executions (they finish with ErrorAborted and message "Sandbox closed"), waits for them to
// exit, and rejects new ones.
func (s *Sandbox) Close() error {
	s.mu.Lock()
	s.closed = true
	running := make([]*execution, 0, len(s.running))
	for e := range s.running {
		running = append(running, e)
	}
	s.mu.Unlock()
	for _, e := range running {
		e.abort("Sandbox closed")
	}
	s.engines.release()
	return nil
}

// engineKey identifies a shared engine: the compilation cache location and the heap limit its runtime is sized for.
type engineKey struct {
	cacheDir    string
	memoryLimit uint32
}

var (
	defaultEnginesMu sync.Mutex
	defaultEngines   = map[engineKey]*engine{}
)

// engineSource loads the compiled QuickJS module, the embedded one or a custom one.
type engineSource struct {
	wasm     func() ([]byte, error)
	cacheDir string

	mu sync.Mutex
	// loaded is the shared engine of a custom module per heap limit, kept after the first successful load. The process
	// owns the engines.
	loaded map[uint32]*engine
}

// wasmEngineLoader loads the module wasm returns (nil: the embedded module) with its on-disk compilation cache in cacheDir.
func wasmEngineLoader(wasm func() ([]byte, error), cacheDir string) engineLoader {
	return (&engineSource{wasm: wasm, cacheDir: cacheDir}).load
}

// engineFor returns the compiled module for VMs with the given heap limit.
func (s *Sandbox) engineFor(ctx context.Context, memoryLimit uint32) (*engine, error) {
	return s.engines.load(ctx, memoryLimit)
}

// load returns the compiled module for VMs with the given heap limit. A failed load is retried on the next call, like
// loadQuickJSWasm.
func (s *engineSource) load(ctx context.Context, memoryLimit uint32) (*engine, error) {
	if s.wasm == nil {
		defaultEnginesMu.Lock()
		defer defaultEnginesMu.Unlock()
		key := engineKey{s.cacheDir, memoryLimit}
		if e := defaultEngines[key]; e != nil {
			return e, nil
		}
		e, err := newEngine(context.WithoutCancel(ctx), QuickJSWasm(), s.cacheDir, memoryLimit)
		if err != nil {
			return nil, err
		}
		defaultEngines[key] = e
		return e, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.loaded[memoryLimit]; e != nil {
		return e, nil
	}
	wasm, err := s.wasm()
	if err != nil {
		return nil, err
	}
	e, err := customEngine(context.WithoutCancel(ctx), wasm, s.cacheDir, memoryLimit)
	if err != nil {
		return nil, err
	}
	if s.loaded == nil {
		s.loaded = map[uint32]*engine{}
	}
	s.loaded[memoryLimit] = e
	return e, nil
}

// release forgets the custom engines the source holds; the process-wide set keeps them for other sandboxes.
func (s *engineSource) release() {
	s.mu.Lock()
	s.loaded = nil
	s.mu.Unlock()
}

// customEngineKey identifies a compiled custom module: its content, the compilation cache location and the heap limit.
type customEngineKey struct {
	digest      [sha256.Size]byte
	cacheDir    string
	memoryLimit uint32
}

var (
	customEnginesMu sync.Mutex
	customEngines   = map[customEngineKey]*engine{}
)

// customEngine compiles a custom module once per process and shares it between the sandboxes that load the same bytes, as Pi's loadQuickJSWasm compiles a module once per process (wasm.ts:14-35) and every
// sandbox given that module uses the one compilation. A failed compile is not kept, so the next call retries it.
func customEngine(ctx context.Context, wasm []byte, cacheDir string, memoryLimit uint32) (*engine, error) {
	key := customEngineKey{sha256.Sum256(wasm), cacheDir, memoryLimit}
	customEnginesMu.Lock()
	defer customEnginesMu.Unlock()
	if e := customEngines[key]; e != nil {
		return e, nil
	}
	e, err := newEngine(ctx, wasm, cacheDir, memoryLimit)
	if err != nil {
		return nil, err
	}
	customEngines[key] = e
	return e, nil
}

// timeoutMessage is upstream's deadline message.
func timeoutMessage(timeoutMs float64) string {
	return "Execution timed out after " + jsnumber.String(timeoutMs) + " ms"
}

// abortMessage is the message of an abort: the context's cause, as upstream uses the abort reason's message.
func abortMessage(ctx context.Context) string {
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) {
		return "This operation was aborted"
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return "The operation was aborted due to timeout"
	}
	return cause.Error()
}
