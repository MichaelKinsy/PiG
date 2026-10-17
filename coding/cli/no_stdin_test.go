package cli

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi #10415: `pi -p hi < <(sleep 15)` never starts because readPipedStdin
// (main.ts:80-97) waits for EOF on a pipe whose writer stays open. PiG's
// --no-stdin skips the read; the default read-until-EOF is unchanged
// (TestReadPipedStdinMatchesUpstreamTrim).
func TestNoStdinStartsWithAnOpenInputPipe(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	// Data that would become part of the prompt if it were read.
	if _, err := writer.WriteString("piped context that must stay unread\n"); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	cmd := modePigCommand(ctx, t, bin, agentDir, "--no-stdin", "-p", "What is 20+22?")
	cmd.Stdin = reader
	out, err := cmd.Output()
	if ctx.Err() != nil {
		t.Fatalf("pig --no-stdin did not exit with its stdin pipe open")
	}
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec.Command returns *ExitError unwrapped
			stderr = string(exitErr.Stderr)
		}
		t.Fatalf("pig --no-stdin: %v: %s", err, stderr)
	}
	if !strings.Contains(string(out), "42") {
		t.Fatalf("stdout = %q, want the faux answer to the prompt", out)
	}
}

func TestParseArgsNoStdin(t *testing.T) {
	if f := parseArgs([]string{"--no-stdin", "-p", "hi"}); !f.NoStdin || len(f.Diagnostics) != 0 || len(f.Messages) != 1 {
		t.Fatalf("parseArgs(--no-stdin -p hi) = %+v", f)
	}
	if f := parseArgs([]string{"-p", "hi"}); f.NoStdin {
		t.Fatal("NoStdin must default to false")
	}
}
