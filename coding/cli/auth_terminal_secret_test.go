package cli

// pi: packages/coding-agent/src/cli/auth-terminal.ts (readSecret's onData handler)

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/terminalquestion"
)

func TestSecretEntryHandlesTypedInput(t *testing.T) {
	tests := []struct {
		name        string
		chunks      []string
		wantValue   string
		wantEnded   bool
		wantInterup bool
	}{
		{name: "carriage return ends the entry", chunks: []string{"abc\r"}, wantValue: "abc", wantEnded: true},
		{name: "line feed ends the entry", chunks: []string{"abc\n"}, wantValue: "abc", wantEnded: true},
		{name: "input after Enter in the same chunk is dropped", chunks: []string{"ab\rcd"}, wantValue: "ab", wantEnded: true},
		{name: "Enter alone ends an empty entry", chunks: []string{"\r"}, wantValue: "", wantEnded: true},
		{name: "chunks accumulate", chunks: []string{"ab", "cd"}, wantValue: "abcd"},
		{name: "DEL removes the last character", chunks: []string{"abc\x7f"}, wantValue: "ab"},
		{name: "backspace removes the last character", chunks: []string{"abc\b\b"}, wantValue: "a"},
		{name: "backspace on an empty entry does nothing", chunks: []string{"\x7f\ba"}, wantValue: "a"},
		{name: "backspace removes one code point, not one byte", chunks: []string{"é🙂\x7f"}, wantValue: "é"},
		{name: "backspace removes a lone astral code point", chunks: []string{"🙂\x7f"}, wantValue: ""},
		{name: "a chunk starting with escape is ignored whole", chunks: []string{"ab", "\x1b[Axyz", "c"}, wantValue: "abc"},
		{name: "escape inside a chunk is only a dropped control character", chunks: []string{"a\x1b[Bc"}, wantValue: "a[Bc"},
		{name: "an escape chunk does not end the entry", chunks: []string{"\x1b\r"}, wantValue: ""},
		{name: "control characters are ignored", chunks: []string{"a\tb\x00c\x1fd"}, wantValue: "abcd"},
		{name: "space is a character", chunks: []string{"a b "}, wantValue: "a b "},
		{name: "tilde and non-ASCII are characters", chunks: []string{"~é\u0085日"}, wantValue: "~é\u0085日"},
		{name: "invalid UTF-8 becomes the replacement character", chunks: []string{"a\xffb"}, wantValue: "a\ufffdb"},
		{name: "Ctrl+C reports an interrupt and does not end the entry", chunks: []string{"ab\x03"}, wantValue: "ab", wantInterup: true},
		{name: "Ctrl+C drops the rest of its chunk, Enter included", chunks: []string{"a\x03b\r"}, wantValue: "a", wantInterup: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var entry secretEntry
			var ended, interrupted bool
			for _, chunk := range tt.chunks {
				ended, interrupted = entry.handle(chunk)
			}
			if got := string(entry.value); got != tt.wantValue || ended != tt.wantEnded || interrupted != tt.wantInterup {
				t.Fatalf("value %q ended %v interrupted %v, want %q %v %v", got, ended, interrupted, tt.wantValue, tt.wantEnded, tt.wantInterup)
			}
		})
	}
}

func TestSecretEntryKeepsTypingAfterACtrlC(t *testing.T) {
	var entry secretEntry
	if ended, interrupted := entry.handle("a\x03b"); ended || !interrupted {
		t.Fatalf("ended %v interrupted %v", ended, interrupted)
	}
	if ended, interrupted := entry.handle("c\r"); !ended || interrupted || string(entry.value) != "ac" {
		t.Fatalf("value %q ended %v interrupted %v", string(entry.value), ended, interrupted)
	}
}

func TestLoginTerminalWriteAndErrorEndEachLineWithANewline(t *testing.T) {
	var out bytes.Buffer
	terminal := processLoginTerminal{reader: terminalquestion.NewReader(bytes.NewReader(nil)), output: &out}
	terminal.Write("first")
	terminal.Write("")
	if got := out.String(); got != "first\n\n" {
		t.Fatalf("Write output %q", got)
	}

	stderr := filepath.Join(t.TempDir(), "stderr")
	file, err := os.Create(stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	previous := os.Stderr
	os.Stderr = file
	defer func() { os.Stderr = previous }()
	terminal.Error("failed")
	terminal.Error("again")
	os.Stderr = previous
	data, err := os.ReadFile(stderr)
	if err != nil || string(data) != "failed\nagain\n" {
		t.Fatalf("Error output %q, %v", data, err)
	}
	if out.String() != "first\n\n" {
		t.Fatalf("Error wrote to the output writer: %q", out.String())
	}
}

func TestLoginTerminalIsNotInteractiveWithoutTerminals(t *testing.T) {
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devnull.Close() }()
	stdin, stdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = devnull, devnull
	defer func() { os.Stdin, os.Stdout = stdin, stdout }()
	if (processLoginTerminal{}).Interactive() {
		t.Fatal("Interactive is true with neither stdin nor stdout a terminal")
	}
}

func TestCreateProcessLoginTerminalUsesProcessStdinStdoutAndTheInterruptHandler(t *testing.T) {
	calls := 0
	created := CreateProcessLoginTerminal(func() { calls++ })
	terminal, ok := created.(processLoginTerminal)
	if !ok {
		t.Fatalf("terminal type %T", created)
	}
	if terminal.reader != terminalquestion.Stdin() {
		t.Fatal("questions do not read the shared stdin reader")
	}
	if output, ok := terminal.output.(*os.File); !ok || output != os.Stdout {
		t.Fatalf("output %v is not stdout", terminal.output)
	}
	terminal.onInterrupt()
	if calls != 1 {
		t.Fatalf("interrupt handler ran %d times", calls)
	}
}
