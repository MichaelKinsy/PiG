package subprocess

import (
	"os"
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
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := goTraceCulprit([]byte(tc.trace), members); got != tc.want {
				t.Fatalf("culprit = %q, want %q", got, tc.want)
			}
		})
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
	if got := traceCulprit([]byte("goroutine 1 [running]:\nexample.test/a.F()\n"), []nativeMember{{name: "a", language: "rust", importPath: "example.test/a"}}); got != "" {
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
