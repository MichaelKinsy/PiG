package codemode

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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
	globals []Tool
	closed  bool
	running map[*execution]struct{}

	// loaded is the engine of a custom Wasm module, kept after the first successful load.
	loadMu sync.Mutex
	loaded *engine
}

// NewSandbox validates the globals and registers the tools.
func NewSandbox(options SandboxOptions) (*Sandbox, error) {
	s := &Sandbox{options: options, running: map[*execution]struct{}{}}
	if s.options.TimeoutMs == 0 {
		s.options.TimeoutMs = defaultTimeoutMs
	}
	for _, tool := range options.Tools {
		if err := s.RegisterTool(tool); err != nil {
			return nil, err
		}
	}
	namespaces := map[string]bool{}
	names := map[string]bool{}
	for _, global := range options.Globals {
		parts := strings.Split(global.Name, ".")
		valid := len(parts) <= 2
		for _, part := range parts {
			valid = valid && isIdentifier(part)
		}
		if !valid || containsString(reservedGlobals, parts[0]) {
			return nil, fmt.Errorf("Invalid global name %q", global.Name)
		}
		if names[global.Name] {
			return nil, fmt.Errorf("Global %q is already registered", global.Name)
		}
		if len(parts) == 2 {
			namespaces[parts[0]] = true
		}
		names[global.Name] = true
		s.globals = append(s.globals, global)
	}
	for _, global := range s.globals {
		if namespaces[global.Name] {
			return nil, fmt.Errorf("Global %q conflicts with the namespace %q", global.Name, global.Name)
		}
	}
	return s, nil
}

func containsString(list []string, s string) bool {
	return slices.Contains(list, s)
}

// RegisterTool fails if a tool with the same name is already registered.
func (s *Sandbox) RegisterTool(tool Tool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.tools {
		if existing.Name == tool.Name {
			return fmt.Errorf("Tool %q is already registered", tool.Name)
		}
	}
	s.tools = append(s.tools, tool)
	return nil
}

// UnregisterTool removes a tool and reports whether it existed.
func (s *Sandbox) UnregisterTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, tool := range s.tools {
		if tool.Name == name {
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
	s.loadMu.Lock()
	loaded := s.loaded
	s.loaded = nil
	s.loadMu.Unlock()
	if loaded != nil {
		return loaded.close(context.Background())
	}
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

// engine returns the compiled module. A failed load is retried on the next call, like loadQuickJSWasm.
func (s *Sandbox) engine(ctx context.Context) (*engine, error) {
	if s.options.Wasm == nil {
		defaultEnginesMu.Lock()
		defer defaultEnginesMu.Unlock()
		key := engineKey{s.options.CacheDir, s.options.MemoryLimitBytes}
		if e := defaultEngines[key]; e != nil {
			return e, nil
		}
		e, err := newEngine(context.WithoutCancel(ctx), QuickJSWasm(), s.options.CacheDir, s.options.MemoryLimitBytes)
		if err != nil {
			return nil, err
		}
		defaultEngines[key] = e
		return e, nil
	}
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	if s.loaded != nil {
		return s.loaded, nil
	}
	wasm, err := s.options.Wasm()
	if err != nil {
		return nil, err
	}
	e, err := newEngine(context.WithoutCancel(ctx), wasm, s.options.CacheDir, s.options.MemoryLimitBytes)
	if err != nil {
		return nil, err
	}
	s.loaded = e
	return e, nil
}

func loadError(err error) *Error {
	return &Error{Kind: ErrorSandbox, Message: "Failed to load QuickJS: " + err.Error()}
}

// timeoutMessage is upstream's deadline message.
func timeoutMessage(timeoutMs float64) string {
	return "Execution timed out after " + jsNumberString(timeoutMs) + " ms"
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
