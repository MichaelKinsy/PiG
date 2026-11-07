package subprocess

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGoTraceCulpritNamesTheMemberWhoseCodeEndedTheProcess(t *testing.T) {
	members := []nativeMember{
		{name: "alpha", language: "go", importPath: "example.test/alpha", modulePath: "example.test/alpha"},
		{name: "beta", language: "go", importPath: "example.test/beta/ext", modulePath: "example.test/beta"},
		{name: "gamma1", language: "go", importPath: "example.test/shared/one", modulePath: "example.test/shared"},
		{name: "gamma2", language: "go", importPath: "example.test/shared/two", modulePath: "example.test/shared"},
	}
	const sdkFrames = "github.com/MichaelKinsy/PiG/extensions/sdk.(*Extension).handle(0xc000010000)\n\t/sdk/extension.go:10 +0x1\n"
	cases := map[string]struct{ trace, want string }{
		"a goroutine the extension started": {
			"panic: boom\n\ngoroutine 23 [running]:\nexample.test/alpha.Extension.func1.1()\n\texample.test/alpha/ext.go:9 +0x25\ncreated by example.test/alpha.Extension.func1 in goroutine 22\n\texample.test/alpha/ext.go:8 +0x1\n",
			"alpha",
		},
		"a method of the member's type, with arguments": {
			"panic: boom\n\ngoroutine 7 [running]:\nexample.test/beta/ext.(*Component).HandleInput(0xc0000a, {0x1, 0x2})\n\texample.test/beta/ext/component.go:30 +0x9\n" + sdkFrames,
			"beta",
		},
		"the member's code under the runtime's panic frames": {
			"panic: boom\n\ngoroutine 5 [running]:\nruntime/debug.Stack()\n\t/go/src/runtime/debug/stack.go:26 +0x5e\npanic({0x6a0, 0xc000})\n\t/go/src/runtime/panic.go:800 +0x132\nexample.test/alpha.work(...)\n\texample.test/alpha/w.go:3\n" + sdkFrames,
			"alpha",
		},
		"only the SDK's frames, created by the SDK": {
			"panic: boom\n\ngoroutine 9 [running]:\n" + sdkFrames + "created by github.com/MichaelKinsy/PiG/extensions/sdk.(*Extension).installEditor in goroutine 1\n\t/sdk/editor.go:386 +0x1\n",
			"",
		},
		"the runner's own frames": {
			"panic: boom\n\ngoroutine 1 [running]:\nmain.main()\n\t/tmp/runner/main.go:40 +0x1\n",
			"",
		},
		"a package of a module two members share": {
			"panic: boom\n\ngoroutine 5 [running]:\nexample.test/shared/helper.Do()\n\t/x.go:1\n",
			"",
		},
		"a subpackage of one member's module": {
			"panic: boom\n\ngoroutine 5 [running]:\nexample.test/beta/internal/util.Do()\n\t/x.go:1\n",
			"beta",
		},
		"only the goroutine that was running counts": {
			"fatal error: concurrent map writes\n\ngoroutine 5 [running]:\nmain.main()\n\t/x.go:1\n\ngoroutine 6 [runnable]:\nexample.test/alpha.Other()\n\t/y.go:1\n",
			"",
		},
		"no trace": {"exit status 2\n", ""},
		// The probes of the review: the shared log holds the blocks the SDK writes for the panics it recovers, and a process can end without a panic.
		"an earlier recovered panic does not decide": {
			"extension: recovered from panic in tool_call \"boom\": reach exploded\ngoroutine 247 [running]:\nruntime/debug.Stack()\n\t/go/src/runtime/debug/stack.go:26 +0x5e\nexample.test/beta/ext.Handler(0xc0)\n\texample.test/beta/ext/h.go:9 +0x1\n" +
				"panic: crash of alpha\n\ngoroutine 23 [running]:\nexample.test/alpha.Extension.func1.1()\n\texample.test/alpha/ext.go:9 +0x25\ncreated by example.test/alpha.Extension.func1 in goroutine 22\n\texample.test/alpha/ext.go:8 +0x1\n",
			"alpha",
		},
		"os.Exit after a recovered panic names no one": {
			"extension: recovered from panic in tool_call \"boom\": reach exploded\ngoroutine 247 [running]:\nruntime/debug.Stack()\n\t/go/src/runtime/debug/stack.go:26 +0x5e\nexample.test/beta/ext.Handler(0xc0)\n\texample.test/beta/ext/h.go:9 +0x1\nexit status 3\n",
			"",
		},
		"a runtime fatal error names no one": {
			"fatal error: all goroutines are asleep - deadlock!\n\ngoroutine 1 [running]:\nexample.test/alpha.Wait()\n\t/x.go:1\n",
			"",
		},
		"a trace that names two members names no one": {
			"panic: boom\n\ngoroutine 5 [running]:\nexample.test/alpha.Run()\n\t/x.go:1\nexample.test/beta/ext.Call(0xc0)\n\t/y.go:2\n",
			"",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := goTraceCulprit([]byte(tc.trace), members, goPanicExitStatus); got != tc.want {
				t.Fatalf("culprit = %q, want %q", got, tc.want)
			}
		})
	}
}

// A section of the log decides only when the process ended the way that section says: the exit status is Go's for a panic, no fatal error follows the panic's
// line, and the panic's message is followed by the running goroutine's header and nothing else.
func TestGoTraceCulpritReadsOnlyWhatEndedTheProcess(t *testing.T) {
	members := []nativeMember{
		{name: "alpha", language: "go", importPath: "example.test/alpha", modulePath: "example.test/alpha"},
		{name: "beta", language: "go", importPath: "example.test/beta/ext", modulePath: "example.test/beta"},
	}
	// What a member writes for a panic it recovered, in the form the Go runtime writes its own.
	const logged = "panic: assignment to entry in nil map\n\ngoroutine 41 [running]:\nruntime/debug.Stack()\n\t/go/src/runtime/debug/stack.go:26 +0x5e\nexample.test/beta/ext.Handler(0xc0)\n\texample.test/beta/ext/h.go:9 +0x1\n"
	const runtimePanic = "panic: crash of alpha\n\ngoroutine 23 [running]:\nexample.test/alpha.Extension.func1.1()\n\texample.test/alpha/ext.go:9 +0x25\ncreated by example.test/alpha.Extension.func1 in goroutine 22\n\texample.test/alpha/ext.go:8 +0x1\n"
	cases := map[string]struct {
		trace string
		exit  int
		want  string
	}{
		"an unrecovered panic that ended with status 2": {logged + runtimePanic, 2, "alpha"},
		"a logged panic, then os.Exit(3)":               {logged + "exit status 3\n", 3, ""},
		"a logged panic, then a kill":                   {logged, -1, ""},
		"a logged panic, then os.Exit(0)":               {logged, 0, ""},
		"a logged panic, then a fatal error, status 2": {
			logged + "fatal error: sync: unlock of unlocked mutex\n\ngoroutine 5 [running]:\nexample.test/alpha.Unlock()\n\t/x.go:1\n", 2, "",
		},
		"a fatal error after a runtime panic section":                                                        {runtimePanic + "fatal error: all goroutines are asleep - deadlock!\n", 2, ""},
		"a logged panic block then os.Exit(2) cannot be told from a runtime section (the limit D274 states)": {logged, 2, "beta"},
		"a panic whose message has continuation lines": {
			"panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x4]\n\ngoroutine 23 [running]:\nexample.test/alpha.Run()\n\t/x.go:1\n", 2, "alpha",
		},
		"a repanic": {
			"panic: first [recovered]\n\tpanic: second\n\ngoroutine 23 [running]:\nexample.test/alpha.Run()\n\t/x.go:1\n", 2, "alpha",
		},
		// The probe of the review: a member's recovered block written between the runtime's panic line and its goroutine header.
		"a block between the panic line and its goroutine header": {
			"panic: crash of alpha\nrecovered: busy\ngoroutine 41 [running]:\nexample.test/beta/ext.Handler(0xc0)\n\t/h.go:9 +0x1\n\ngoroutine 23 [running]:\nexample.test/alpha.Run()\n\t/x.go:1\n", 2, "",
		},
		"text between the panic's message and its goroutine header": {
			"panic: crash of alpha\n\nrecovered: busy\ngoroutine 23 [running]:\nexample.test/alpha.Run()\n\t/x.go:1\n", 2, "",
		},
		"a panic with no goroutine header": {"panic: crash of alpha\n", 2, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := goTraceCulprit([]byte(tc.trace), members, tc.exit); got != tc.want {
				t.Fatalf("culprit = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProcessExitCode(t *testing.T) {
	if got := processExitCode(nil); got != 0 {
		t.Fatalf("a clean exit = %d", got)
	}
	if got := processExitCode(errors.New("wait failed")); got != -1 {
		t.Fatalf("a wait error = %d", got)
	}
	// A test binary that is given a flag it does not know exits with status 2.
	if got := processExitCode(exec.Command(os.Args[0], "-test.nosuchflag").Run()); got != 2 {
		t.Fatalf("a child that exited with status 2 = %d", got)
	}
}

func TestLispTraceCulpritNamesTheMemberWhosePackageQualifiesAFrame(t *testing.T) {
	members := []nativeMember{
		{name: "alpha", language: "lisp", lispPkg: "ALPHA"},
		{name: "my-alpha", language: "lisp", lispPkg: "MY-ALPHA"},
	}
	cases := map[string]struct{ trace, want string }{
		"a thread the extension made": {
			"Unhandled SIMPLE-ERROR in thread #<SB-THREAD:THREAD \"worker\" RUNNING {1001}>:\n  boom\n\nBacktrace for: #<SB-THREAD:THREAD \"worker\" RUNNING {1001}>\n0: (SB-DEBUG::DEBUGGER-DISABLED-HOOK #<SIMPLE-ERROR \"boom\" {1002}> #<unavailable argument>)\n4: (ERROR \"boom\")\n5: ((LAMBDA NIL :IN ALPHA::NEW-EXTENSION))\n6: ((FLET SB-THREAD::WITH-MUTEX-FUNCTION :IN SB-THREAD::RUN))\n",
			"alpha",
		},
		"a package whose name ends in another's": {
			"Backtrace for: #<SB-THREAD:THREAD>\n3: (MY-ALPHA::RUN)\n",
			"my-alpha",
		},
		"a package whose name is lower case": {
			"Backtrace for: #<SB-THREAD:THREAD tid=424 RUNNING {1201658003}>\n3: (ERROR \"crash of culprit\")\n4: ((LAMBDA NIL :IN |alpha|::NEW-EXTENSION))\n",
			"alpha",
		},
		"only the SDK": {
			"Backtrace for: #<SB-THREAD:THREAD>\n3: (PIG::WIDTH-WORKER-LOOP #<PIG::EXTENSION {1}>)\n",
			"",
		},
		"lines before the backtrace do not count": {
			"extension ALPHA:X failed\nBacktrace for: #<SB-THREAD:THREAD>\n3: (PIG::LOOP)\n",
			"",
		},
		"frames before the backtrace do not count": {
			"4: ((LAMBDA NIL :IN ALPHA::NEW-EXTENSION))\nBacktrace for: #<SB-THREAD:THREAD>\n3: (PIG::LOOP)\n",
			"",
		},
		"the condition's text names no one": {
			"Backtrace for: #<SB-THREAD:THREAD>\n3: (ERROR \"alpha: connection lost\")\n4: (PIG::WIDTH-WORKER-LOOP #<PIG::EXTENSION {1}>)\n",
			"",
		},
		"a trace that names two members names no one": {
			"Backtrace for: #<SB-THREAD:THREAD>\n3: (ALPHA::RUN)\n4: ((LAMBDA NIL :IN MY-ALPHA::NEW-EXTENSION))\n",
			"",
		},
		"only the last backtrace decides": {
			"Backtrace for: #<SB-THREAD:THREAD>\n3: (MY-ALPHA::RUN)\nBacktrace for: #<SB-THREAD:THREAD>\n3: (ALPHA::RUN)\n",
			"alpha",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := lispTraceCulprit([]byte(tc.trace+lispQuitLine+"\n"), members); got != tc.want {
				t.Fatalf("culprit = %q, want %q", got, tc.want)
			}
		})
	}
}

// A backtrace decides only when SBCL's quit line follows it: a member that printed one itself and a process that then ended another way name no one.
func TestLispTraceCulpritReadsOnlyWhatEndedTheProcess(t *testing.T) {
	members := []nativeMember{
		{name: "alpha", language: "lisp", lispPkg: "ALPHA"},
		{name: "beta", language: "lisp", lispPkg: "BETA"},
	}
	const printed = "Backtrace for: #<SB-THREAD:THREAD tid=24 RUNNING {1201}>\n0: (BETA::HELPER)\n1: (SB-INT:SIMPLE-EVAL-IN-LEXENV (BETA::HELPER) #<NULL-LEXENV>)\n"
	const unhandled = "Unhandled SIMPLE-ERROR in thread #<SB-THREAD:THREAD tid=22 RUNNING {1200}>:\n  boom\n\nBacktrace for: #<SB-THREAD:THREAD tid=22 RUNNING {1200}>\n3: (ERROR \"boom\")\n4: ((LAMBDA NIL :IN ALPHA::NEW-EXTENSION))\n\n" + lispQuitLine + "\n"
	cases := map[string]struct{ trace, want string }{
		"an unhandled condition":                           {unhandled, "alpha"},
		"a printed backtrace, then an unhandled condition": {printed + unhandled, "alpha"},
		"a printed backtrace, then sb-ext:exit or a kill":  {printed, ""},
		"a printed backtrace after the quit line":          {unhandled + printed, ""},
		"a quit line before any backtrace":                 {lispQuitLine + "\n", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := lispTraceCulprit([]byte(tc.trace), members); got != tc.want {
				t.Fatalf("culprit = %q, want %q", got, tc.want)
			}
		})
	}
}

// A trace names a member only for a language whose threads the host can read; a Python or Rust process does not end with a thread.
func TestTraceCulpritReadsOnlyGoAndLisp(t *testing.T) {
	if got := traceCulprit([]byte("goroutine 1 [running]:\nexample.test/a.F()\n"), []nativeMember{{name: "a", language: "rust", importPath: "example.test/a"}}, 2); got != "" {
		t.Fatalf("culprit = %q", got)
	}
}

// Two members of a cell fail: each load error carries its own cause, whichever the process wrote last, and not the panic lines a runtime prints between them.
func TestStderrCauseNamesEachFailingMembersOwnLine(t *testing.T) {
	log := "thread '<unnamed>' (1) panicked at src/lib.rs:3:2:\nfirst boom\nnote: run with `RUST_BACKTRACE=1`\nthread '<unnamed>' (2) panicked at src/lib.rs:3:2:\nsecond boom\n" +
		"extension \"first\" load failed: panicked at src/lib.rs:3:2: first boom\n\tpanic stack\nextension \"second\" load failed: factory returned nil\nextension \"first\" load failed: panicked at src/lib.rs:3:2: first boom\n"
	path := filepath.Join(t.TempDir(), "stderr.log")
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stderrCause(path, "first"); got != "panicked at src/lib.rs:3:2: first boom" {
		t.Fatalf("first cause = %q", got)
	}
	if got := stderrCause(path, "second"); got != "factory returned nil" {
		t.Fatalf("second cause = %q", got)
	}
}
