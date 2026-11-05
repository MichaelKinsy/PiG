package codemode

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// cachedFiles counts the files below dir: what wazero's compilation cache wrote there.
func cachedFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func runOnce(t *testing.T, options SandboxOptions, code, want string) {
	t.Helper()
	sandbox, err := NewSandbox(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	result, err := sandbox.Execute(t.Context(), code, ExecuteOptions{})
	if err != nil || !result.OK || string(result.Value) != want {
		t.Fatalf("result = %+v (%+v), %v; want %s", result, result.Error, err, want)
	}
}

// pig additive (D263): SandboxOptions.Interpreter selects wazero's interpreter, and the compiler stays the default. The runtime
// configuration shows in the compilation cache: the compiler writes its machine code under CacheDir, the interpreter keeps no
// compiled code and leaves the directory empty (its CacheDir is ignored, not an error).
func TestSandboxOptionInterpreterSelectsTheInterpreterRuntime(t *testing.T) {
	compiled, interpreted := t.TempDir(), t.TempDir()
	runOnce(t, SandboxOptions{CacheDir: compiled, MemoryLimitBytes: 48 << 20}, "return 6 * 7", "42")
	if cachedFiles(t, compiled) == 0 {
		t.Fatal("the default engine wrote nothing to CacheDir: it is not the compiler, or the cache is not used")
	}
	runOnce(t, SandboxOptions{CacheDir: interpreted, MemoryLimitBytes: 48 << 20, Interpreter: true}, "return 6 * 7", "42")
	if n := cachedFiles(t, interpreted); n != 0 {
		t.Fatalf("the interpreter wrote %d files to CacheDir", n)
	}
}

// A compiler engine and an interpreter engine for the same module, cache directory and heap limit are two engines, for the
// embedded module and for a custom one.
func TestInterpreterAndCompilerEnginesAreNotShared(t *testing.T) {
	open := func(options SandboxOptions) *Sandbox {
		t.Helper()
		sandbox, err := NewSandbox(options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sandbox.Close() })
		return sandbox
	}
	engineOf := func(s *Sandbox) *engine {
		t.Helper()
		e, err := s.engineFor(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	custom := func() ([]byte, error) { return QuickJSWasm(), nil }
	for name, wasm := range map[string]func() ([]byte, error){"embedded": nil, "custom": custom} {
		compiler := engineOf(open(SandboxOptions{Wasm: wasm}))
		interpreter := engineOf(open(SandboxOptions{Wasm: wasm, Interpreter: true}))
		if compiler == interpreter {
			t.Errorf("%s: a compiler and an interpreter sandbox share one engine", name)
		}
		if again := engineOf(open(SandboxOptions{Wasm: wasm, Interpreter: true})); again != interpreter {
			t.Errorf("%s: two interpreter sandboxes compiled the module separately", name)
		}
	}
}

// The interpreter runs scripts to the same results: values, output, a tool call, a thrown error, and the deadline.
func TestInterpreterSandboxRunsScripts(t *testing.T) {
	runOnce(t, SandboxOptions{Interpreter: true}, "text('hi'); return [1, 2, 3].map(x => x * 2)", "[2,4,6]")
	sandbox, err := NewSandbox(SandboxOptions{Interpreter: true, TimeoutMs: 1500})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	result, err := sandbox.Execute(t.Context(), "while (true) {}", ExecuteOptions{})
	if err != nil || result.OK || result.Error == nil || result.Error.Kind != ErrorTimeout {
		t.Fatalf("an endless script under the interpreter: %+v (%+v), %v; want a timeout", result, result.Error, err)
	}
}

// The one script result the two engines do not share is unbounded recursion: the compiler's VM hits QuickJS's own stack limit and
// the script catches a RangeError, while wazero's interpreter stops the VM at its call-depth limit first, which fails the execution
// as a sandbox error. The difference shows which runtime ran the script, whatever CacheDir holds.
func TestInterpreterRunsDeepRecursionDifferentlyFromTheCompiler(t *testing.T) {
	const code = `let depth = 0;
function dive() { depth++; dive(); }
try { dive(); } catch (error) { return [depth > 1000, error.name]; }`
	for _, interpreter := range []bool{false, true} {
		sandbox, err := NewSandbox(SandboxOptions{Interpreter: interpreter, TimeoutMs: 120000})
		if err != nil {
			t.Fatal(err)
		}
		result, err := sandbox.Execute(t.Context(), code, ExecuteOptions{})
		_ = sandbox.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !interpreter {
			if !result.OK || string(result.Value) != `[true,"RangeError"]` {
				t.Errorf("compiler: result = %+v (%+v), want [true,\"RangeError\"]", result, result.Error)
			}
			continue
		}
		if result.OK || result.Error == nil || result.Error.Kind != ErrorSandbox || !strings.Contains(result.Error.Message, "wasm error: stack overflow") {
			t.Errorf("interpreter: result = %+v (%+v), want a sandbox error naming a wasm stack overflow", result, result.Error)
		}
	}
}
