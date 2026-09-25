package codingagent

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestKeyReleaseDoesNotDuplicateInput asserts the resulting value after a
// complete key event, rather than asserting that a release is merely
// recognizable.
//
// The distinction matters: TestTermProfileMatrix_ReleasesDropped already
// checked tui.IsKeyRelease on the same byte sequences and passed, while two
// consumers that never called it were doubling every keystroke. A predicate
// test proves the predicate; only an outcome test proves the consumer.
//
// pig requests Kitty keyboard flag 2, so a terminal reports press and release
// for every key. Any consumer that dispatches raw input must act on the press
// alone.
func TestKeyReleaseDoesNotDuplicateInput(t *testing.T) {
	// Kitty releases for 'a' and for ctrl+shift+left. Both are what a terminal
	// such as Ghostty actually emits alongside the press.
	const (
		pressA         = "a"
		releaseA       = "\x1b[97;1:3u"
		releaseALong   = "\x1b[97::97;1:3u"
		releaseCtrlSLt = "\x1b[1;6:3D"
	)

	t.Run("extension input prompt keeps one character per keystroke", func(t *testing.T) {
		for _, release := range []string{releaseA, releaseALong} {
			component := tui.NewExtensionInputComponent("title", "placeholder")
			// Drive the production input pump: press, release, then Enter.
			source := strings.NewReader(pressA + release + "\r")
			driveModalInputFromReader(t, component, source)
			if got := component.Text(); got != "a" {
				t.Fatalf("release %q produced %q, want \"a\": the prompt doubles typed characters", release, got)
			}
		}
	})

	t.Run("modal delivery keeps one character per keystroke", func(t *testing.T) {
		component := tui.NewExtensionInputComponent("title", "placeholder")
		var b StdinBuffer
		chunks := dropKeyReleases(component, b.ProcessString(pressA+releaseA))
		if len(chunks) != 1 {
			t.Fatalf("one keystroke yielded %d chunks: %q", len(chunks), chunks)
		}
		if chunks[0] != pressA {
			t.Fatalf("delivered %q, want the press %q", chunks[0], pressA)
		}
	})

	t.Run("extension shortcut fires once per keystroke", func(t *testing.T) {
		fired := 0
		m := &InteractiveMode{}
		m.extensionShortcutListener = func(data string) bool {
			if tui.MatchesKeyID(data, "ctrl+shift+left") {
				fired++
				return true
			}
			return false
		}
		m.notifyTerminalInput("\x1b[1;6D")
		m.notifyTerminalInput(releaseCtrlSLt)
		if fired != 1 {
			t.Fatalf("shortcut fired %d times for one keystroke, want 1", fired)
		}
	})
}

type chunkReader struct {
	chunks [][]byte
}

func (r *chunkReader) Read(dst []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(dst, chunk), nil
}

func driveModalInputFromReader(t *testing.T, component *tui.ExtensionInputComponent, source io.Reader) {
	t.Helper()
	m := &InteractiveMode{}
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	readCh := make(chan inputChunk, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		m.pumpTerminalInput(ctx, source, readCh, errCh)
	}()
	for !component.Done() {
		buf := <-inputCh
		dispatchModalInput(component, []string{string(buf)}, component.HandleInput, component.Done)
	}
	cancel()
	<-pumpDone
}

// Upstream ProcessTerminal emits every ordinary character as its own input
// event. A modal must therefore apply every Backspace in one terminal read.
func TestExtensionInputSplitsBatchedOrdinaryKeys(t *testing.T) {
	component := tui.NewExtensionInputComponent("title", "placeholder")
	source := &chunkReader{chunks: [][]byte{[]byte("abcd"), []byte("\x7f\x7f"), []byte("\r")}}
	driveModalInputFromReader(t, component, source)
	if got := component.Text(); got != "ab" {
		t.Fatalf("batched Backspace input produced %q, want %q", got, "ab")
	}
}
