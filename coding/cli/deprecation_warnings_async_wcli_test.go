package cli

// pi: packages/coding-agent/src/migrations.ts

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

var deprecationANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

// recordedLog is the ordered record of what the acknowledgement did around the key wait.
type recordedLog struct {
	out    bytes.Buffer
	events []string
}

func (l *recordedLog) Write(p []byte) (int, error) {
	l.events = append(l.events, "write")
	return l.out.Write(p)
}

// Pi migrations.ts:277-296 showDeprecationWarnings: print the warnings and the
// two links, then switch stdin to raw mode and block on one keypress (no
// timeout, no cancellation), restore the mode, and print an empty line.
func TestDeprecationWarningsPrintThenWaitForOneKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, w := io.Pipe()
		defer func() { _ = r.Close(); _ = w.Close() }()
		log := &recordedLog{}
		rawMode := func() (func() error, error) {
			log.events = append(log.events, "raw-on")
			return func() error { log.events = append(log.events, "raw-off"); return nil }, nil
		}
		done := make(chan error, 1)
		go func() {
			done <- awaitDeprecationAcknowledgement(log, r, rawMode, []string{"Global extension: pre-0.35 layout", "Project extension: other"})
		}()
		synctest.Wait()
		want := "Warning: Global extension: pre-0.35 layout\nWarning: Project extension: other\n" +
			"\nMove your extensions to the extensions/ directory.\n" +
			"Migration guide: " + codingagent.MigrationGuideURL + "\n" +
			"Documentation: " + codingagent.ExtensionsDocURL + "\n" +
			"\nPress any key to continue...\n"
		if got := deprecationANSI.ReplaceAllString(log.out.String(), ""); got != want {
			t.Fatalf("printed before the key:\n%q\nwant\n%q", got, want)
		}
		select {
		case err := <-done:
			t.Fatalf("returned (%v) before any key arrived", err)
		default:
		}
		if last := log.events[len(log.events)-1]; last != "raw-on" {
			t.Fatalf("events before the key = %v; raw mode must be on while waiting", log.events)
		}
		before := len(log.events)
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := log.events[before:]; len(got) != 2 || got[0] != "raw-off" || got[1] != "write" {
			t.Fatalf("events after the key = %v, want raw mode restored, then the closing blank line", got)
		}
		if !strings.HasSuffix(log.out.String(), "continue...\n\n") && !strings.HasSuffix(deprecationANSI.ReplaceAllString(log.out.String(), ""), "continue...\n\n") {
			t.Fatalf("output %q does not end with the blank line", log.out.String())
		}
	})
}

type failIfRead struct{ t *testing.T }

func (f failIfRead) Read([]byte) (int, error) {
	f.t.Error("stdin was read although there is nothing to acknowledge")
	return 0, io.EOF
}

// Pi :278: no warnings, no output, no wait, no raw mode.
func TestDeprecationWarningsAreSilentWithoutWarnings(t *testing.T) {
	var out bytes.Buffer
	rawMode := func() (func() error, error) {
		t.Error("raw mode switched on with nothing to acknowledge")
		return func() error { return nil }, nil
	}
	if err := awaitDeprecationAcknowledgement(&out, failIfRead{t}, rawMode, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("printed %q", out.String())
	}
}

// A raw-mode failure surfaces before anything waits, and a stdin that ends
// without a key still restores the terminal and lets startup go on.
func TestDeprecationWarningsRawModeFailureAndEndedInput(t *testing.T) {
	failure := errors.New("not a tty")
	var out bytes.Buffer
	err := awaitDeprecationAcknowledgement(&out, failIfRead{t}, func() (func() error, error) { return nil, failure }, []string{"w"})
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the raw-mode failure", err)
	}
	restored := false
	err = awaitDeprecationAcknowledgement(&out, strings.NewReader(""), func() (func() error, error) {
		return func() error { restored = true; return nil }, nil
	}, []string{"w"})
	if err != nil || !restored {
		t.Fatalf("err = %v, restored = %v; an ended stdin must restore the terminal and return", err, restored)
	}
}
