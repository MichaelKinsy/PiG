package terminalquestion

// Ports packages/coding-agent/test/terminal-question.test.ts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestQuestionWritesToTheSelectedOutputAndReturnsTheAnswer(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	var output bytes.Buffer
	interrupts := 0
	reader := NewReader(input)
	go func() { _, _ = writer.Write([]byte("https://example.test/callback\n")) }()
	got, err := reader.Question(t.Context(), "Paste URL: ", &output, func() { interrupts++ })
	if err != nil || got != "https://example.test/callback" {
		t.Fatalf("got %q, %v", got, err)
	}
	if output.String() != "Paste URL: " || interrupts != 0 {
		t.Fatalf("output %q, interrupts %d", output.String(), interrupts)
	}
}

func TestQuestionFailsOnEOFWithoutRequiringAnInterruptCallback(t *testing.T) {
	input, writer := io.Pipe()
	reader := NewReader(input)
	go func() { _ = writer.Close() }()
	if _, err := reader.Question(t.Context(), "Paste URL: ", io.Discard, nil); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
}

func TestQuestionKeepsAnAnswerDeliveredImmediatelyBeforeEOF(t *testing.T) {
	reader := NewReader(bytes.NewReader([]byte("example.com\n")))
	interrupts := 0
	got, err := reader.Question(t.Context(), "Enter domain: ", io.Discard, func() { interrupts++ })
	if err != nil || got != "example.com" || interrupts != 0 {
		t.Fatalf("got %q, %v, interrupts %d", got, err, interrupts)
	}
}

func TestCtrlCFailsTheQuestionAndReportsAnInterrupt(t *testing.T) {
	reader := NewReader(bytes.NewReader([]byte("\x03")))
	interrupts := 0
	if _, err := reader.Question(t.Context(), "Paste URL: ", io.Discard, func() { interrupts++ }); !errors.Is(err, ErrInterrupted) || interrupts != 1 {
		t.Fatalf("err = %v, interrupts %d", err, interrupts)
	}
}

func TestEOFReportsAnInterrupt(t *testing.T) {
	reader := NewReader(bytes.NewReader(nil))
	interrupts := 0
	if _, err := reader.Question(t.Context(), "Enter domain: ", io.Discard, func() { interrupts++ }); !errors.Is(err, ErrInterrupted) || interrupts != 1 {
		t.Fatalf("err = %v, interrupts %d", err, interrupts)
	}
}

func TestCallerCancellationDoesNotReportAnInterruptAndKeepsTheLaterAnswer(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	reader := NewReader(input)
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("callback won")
	interrupts := 0
	go cancel(cause)
	if _, err := reader.Question(ctx, "Paste URL: ", io.Discard, func() { interrupts++ }); !errors.Is(err, cause) || interrupts != 0 {
		t.Fatalf("err = %v, interrupts %d", err, interrupts)
	}
	// The read of the cancelled question is still in flight: the next question takes it over.
	go func() { _, _ = writer.Write([]byte("later\n")) }()
	got, err := reader.Question(t.Context(), "Again: ", io.Discard, nil)
	if err != nil || got != "later" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAnAlreadyCancelledQuestionFailsAndReportsNoInterrupt(t *testing.T) {
	reader := NewReader(bytes.NewReader([]byte("never\n")))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	interrupts := 0
	if _, err := reader.Question(ctx, "Paste URL: ", io.Discard, func() { interrupts++ }); !errors.Is(err, context.Canceled) || interrupts != 0 {
		t.Fatalf("err = %v, interrupts %d", err, interrupts)
	}
}
