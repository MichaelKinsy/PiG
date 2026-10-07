//go:build darwin

package nativeplatform

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const stackMoveChildEnv = "PIG_NATIVEPLATFORM_STACK_MOVE_CHILD"

// stackMoveDepths covers three doublings of a goroutine stack on darwin/arm64, where an efence stack starts at one 16 KiB page, and five on darwin/amd64 with 4 KiB pages, so at some depths the stack grows between an operation's conversion of its Go array and its system call.
const stackMoveDepths = 2600

// The Go arrays that pasteboardImage and pasteboardFilePaths pass to arrayWithObjects:count:, and the UTF-8 bytes setPasteboardText passes to initWithBytes:length:encoding:, which escape analysis can also leave on the stack, must stay valid when the goroutine stack moves during the message send. With GODEBUG=efence=1 the runtime makes a stack inaccessible once it has copied it, so an address into the old stack faults instead of reading a stale copy that still holds the right values. Each operation runs at the bottom of a fresh goroutine below every depth up to stackMoveDepths. Uses a unique named pasteboard, never the user's general one.
func TestDarwinPasteboardBuffersSurviveAStackMove(t *testing.T) {
	s := loadDarwinClipboard()
	if s == nil || s.fileURLsOnlyKey == 0 {
		t.Skip("AppKit pasteboard symbols are unavailable")
	}
	if os.Getenv(stackMoveChildEnv) == "" {
		cmd := exec.CommandContext(testbudget.Context(t), os.Args[0], "-test.run=^TestDarwinPasteboardBuffersSurviveAStackMove$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), stackMoveChildEnv+"=1", "GODEBUG=efence=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestDarwinPasteboardBuffersSurviveAStackMove") {
			t.Fatalf("the efence run failed: %v\n%s", err, out)
		}
		return
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := syscall_syscall(s.pushPool, 0, 0, 0)
	defer func() { _, _, _ = syscall_syscall(s.popPool, pool, 0, 0) }()
	board := s.send(s.typeNamed("NSPasteboard"), "pasteboardWithUniqueName", 0, 0, 0)
	if board == 0 {
		t.Fatal("could not create a unique pasteboard")
	}
	defer s.send(board, "releaseGlobally", 0, 0, 0)

	if err := s.setPasteboardText(board, "pig"); err != nil {
		t.Fatalf("setPasteboardText = %v", err)
	}
	operations := []struct {
		name string
		run  func() error
	}{
		{"pasteboardImage", func() error {
			if data, err := s.pasteboardImage(board); err != nil || data != nil {
				return fmt.Errorf("image of a text pasteboard = %d bytes, %w; want none", len(data), err)
			}
			return nil
		}},
		{"pasteboardFilePaths", func() error {
			if paths, err := s.pasteboardFilePaths(board); err != nil || paths != nil {
				return fmt.Errorf("file paths of a text pasteboard = %q, %w; want none", paths, err)
			}
			return nil
		}},
		{"setPasteboardText", func() error {
			if err := s.setPasteboardText(board, "pig"); err != nil {
				return err
			}
			if text, err := s.pasteboardText(board); err != nil || text == nil || *text != "pig" {
				return fmt.Errorf("pasteboard text = %s, %w; want %q", quoted(text), err, "pig")
			}
			return nil
		}},
	}
	for _, operation := range operations {
		for depth := range stackMoveDepths {
			if err := onFreshStack(s, depth, operation.run); err != nil {
				t.Fatalf("%s at depth %d: %v", operation.name, depth, err)
			}
		}
	}
}

// onFreshStack runs f in a new goroutine below depth recursive frames, on a locked thread inside its own autorelease pool.
func onFreshStack(s *darwinClipboardSymbols, depth int, f func() error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		pool, _, _ := syscall_syscall(s.pushPool, 0, 0, 0)
		defer func() { _, _, _ = syscall_syscall(s.popPool, pool, 0, 0) }()
		done <- belowFrames(depth, f)
	}()
	return <-done
}

//go:noinline
func belowFrames(depth int, f func() error) error {
	if depth == 0 {
		return f()
	}
	return belowFrames(depth-1, f)
}

func TestDarwinPasteboardTextRoundTrip(t *testing.T) {
	s := loadDarwinClipboard()
	if s == nil {
		t.Skip("AppKit pasteboard symbols are unavailable")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := syscall_syscall(s.pushPool, 0, 0, 0)
	defer func() { _, _, _ = syscall_syscall(s.popPool, pool, 0, 0) }()
	board := s.send(s.typeNamed("NSPasteboard"), "pasteboardWithUniqueName", 0, 0, 0)
	if board == 0 {
		t.Fatal("could not create a unique pasteboard")
	}
	defer s.send(board, "releaseGlobally", 0, 0, 0)

	if text, err := s.pasteboardText(board); err != nil || text != nil {
		t.Fatalf("empty pasteboard text = %s, %v; want none", quoted(text), err)
	}
	for _, want := range []string{"pig", "größer 🐷 als ein Ferkel"} {
		if err := s.setPasteboardText(board, want); err != nil {
			t.Fatalf("setPasteboardText(%q) = %v", want, err)
		}
		if text, err := s.pasteboardText(board); err != nil || text == nil || *text != want {
			t.Fatalf("pasteboard text = %s, %v; want %q", quoted(text), err, want)
		}
	}
}

func quoted(text *string) string {
	if text == nil {
		return "<nil>"
	}
	return strconv.Quote(*text)
}
