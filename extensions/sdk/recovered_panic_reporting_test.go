package sdk

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = writer
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		done <- string(data)
	}()
	func() {
		defer func() {
			os.Stderr = saved
			_ = writer.Close()
		}()
		fn()
	}()
	out := <-done
	_ = reader.Close()
	return out
}

// Pi's terminal input listener propagates a throw to the TUI input loop. An
// out-of-process extension cannot throw into the host, so the recovered panic
// is reported in the extension log and the handler yields no verdict.
func TestTerminalInputHandlerPanicIsReportedAndYieldsNoVerdict(t *testing.T) {
	var result TerminalInputResult
	out := captureStderr(t, func() {
		result = runTerminalInputHandler(func(string) TerminalInputResult { panic("handler exploded") }, "x")
	})
	if result.Consume || result.Data != nil {
		t.Fatalf("result = %+v, want no verdict", result)
	}
	if !strings.Contains(out, "a terminal input handler") || !strings.Contains(out, "handler exploded") {
		t.Fatalf("stderr = %q, want the recovered panic named and its value", out)
	}
}

func TestInvalidatorPanicOnStopIsReported(t *testing.T) {
	overlay := newRemoteOverlay(&panickingInvalidator{})
	overlay.active.Store(true)
	var stopped bool
	out := captureStderr(t, func() { stopped = overlay.stop() })
	if !stopped {
		t.Fatal("overlay did not stop")
	}
	if !strings.Contains(out, "SetInvalidate(nil)") || !strings.Contains(out, "attach failed") {
		t.Fatalf("stderr = %q, want the SetInvalidate panic and its value", out)
	}
}

type comparableOverlay struct{ id int }

type sliceOverlay struct{ rows []string }

type holderOverlay struct{ inner any }

// A mounted component that Go cannot compare is never the target; the answer
// comes from the value's comparability, not from a recovered panic.
func TestSameOverlayComponentNeverPanicsOnUncomparableValues(t *testing.T) {
	shared := &comparableOverlay{id: 1}
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"same pointer", shared, shared, true},
		{"different pointers", shared, &comparableOverlay{id: 1}, false},
		{"equal comparable values", comparableOverlay{id: 2}, comparableOverlay{id: 2}, true},
		{"uncomparable struct", sliceOverlay{rows: []string{"a"}}, sliceOverlay{rows: []string{"a"}}, false},
		{"interface field holding a slice", holderOverlay{inner: []string{"a"}}, holderOverlay{inner: []string{"a"}}, false},
		{"mixed dynamic types in a field", holderOverlay{inner: 1}, holderOverlay{inner: []string{"a"}}, false},
		{"different types", comparableOverlay{id: 1}, sliceOverlay{}, false},
		{"nil and nil", nil, nil, true},
		{"nil and value", nil, shared, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameOverlayComponent(tc.a, tc.b); got != tc.want {
				t.Errorf("sameOverlayComponent = %v, want %v", got, tc.want)
			}
		})
	}
}
