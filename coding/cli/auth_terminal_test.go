package cli

// Ports packages/coding-agent/test/auth-terminal.test.ts

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/terminalquestion"
)

func setupAuthTerminal(t *testing.T, input io.Reader) (processLoginTerminal, context.Context, *int) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	interrupts := 0
	terminal := processLoginTerminal{
		reader:      terminalquestion.NewReader(input),
		output:      io.Discard,
		onInterrupt: func() { interrupts++; cancel() },
	}
	return terminal, ctx, &interrupts
}

func TestAuthTerminalCancelsTheLoginWhenStdinClosesBeforeAnAnswer(t *testing.T) {
	input, writer := io.Pipe()
	terminal, ctx, interrupts := setupAuthTerminal(t, input)
	go func() { _ = writer.Close() }()
	if _, err := terminal.Question(ctx, "Enter domain: "); !errors.Is(err, terminalquestion.ErrInterrupted) {
		t.Fatalf("err = %v", err)
	}
	if *interrupts != 1 || ctx.Err() == nil {
		t.Fatalf("interrupts %d, login ended %t", *interrupts, ctx.Err() != nil)
	}
}

func TestAuthTerminalNormalCleanupDoesNotCancelTheLoginAfterAnAnswer(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	terminal, ctx, interrupts := setupAuthTerminal(t, input)
	go func() { _, _ = writer.Write([]byte("example.com\n")) }()
	got, err := terminal.Question(ctx, "Enter domain: ")
	if err != nil || got != "example.com" || *interrupts != 0 || ctx.Err() != nil {
		t.Fatalf("got %q, %v, interrupts %d, ended %v", got, err, *interrupts, ctx.Err())
	}
}

func TestAuthTerminalKeepsAnAnswerDeliveredImmediatelyBeforeEOF(t *testing.T) {
	input, writer := io.Pipe()
	terminal, ctx, interrupts := setupAuthTerminal(t, input)
	go func() {
		_, _ = writer.Write([]byte("example.com\n"))
		_ = writer.Close()
	}()
	got, err := terminal.Question(ctx, "Enter domain: ")
	if err != nil || got != "example.com" || *interrupts != 0 {
		t.Fatalf("got %q, %v, interrupts %d", got, err, *interrupts)
	}
}

func TestAuthTerminalPromptCancellationDoesNotCancelTheWholeLogin(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	terminal, ctx, interrupts := setupAuthTerminal(t, input)
	prompt, cancelPrompt := context.WithCancel(ctx)
	go cancelPrompt()
	if _, err := terminal.Question(prompt, "Paste code: "); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if *interrupts != 0 || ctx.Err() != nil {
		t.Fatalf("interrupts %d, login ended %v", *interrupts, ctx.Err())
	}
}
