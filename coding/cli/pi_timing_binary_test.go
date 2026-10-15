package cli

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// runPigWithTiming runs print mode with PI_TIMING=env (unset when env is empty) and returns stderr.
func runPigWithTiming(t *testing.T, bin, env string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	cmd := modePigCommand(ctx, t, bin, t.TempDir(), "-p", "reply with exactly: timed-ok")
	cmd.Env = append(cmd.Env, "PI_TIMING="+env)
	if env == "" {
		cmd.Env = append(cmd.Env, "PI_TIMING=")
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdin = devNull
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig: %v\nstdout: %q\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if stdout.String() != "timed-ok\n" {
		t.Fatalf("stdout = %q, want the reply: timing output belongs on stderr", stdout.String())
	}
	return stderr.String()
}

// timings.ts (PI_TIMING=1): the startup marks main.ts records print to stderr before print mode runs, in Pi's report shape: a blank line, the
// "--- Startup Timings: main ---" title, one "  label: Nms" row per mark, "  TOTAL: Nms" as their sum, and title.length+8 dashes.
func TestPiTimingPrintsPisStartupReport(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	stderr := runPigWithTiming(t, bin, "1")
	const title = "Startup Timings: main"
	start := strings.Index(stderr, "\n--- "+title+" ---\n")
	if start < 0 {
		t.Fatalf("no main report on stderr: %q", stderr)
	}
	lines := strings.Split(stderr[start+1:], "\n")
	if lines[0] != "--- "+title+" ---" {
		t.Fatalf("title line = %q", lines[0])
	}
	row := regexp.MustCompile(`^  (.+): (\d+)ms$`)
	var labels []string
	sum := 0
	i := 1
	for ; i < len(lines) && row.MatchString(lines[i]); i++ {
		m := row.FindStringSubmatch(lines[i])
		if m[1] == "TOTAL" {
			break
		}
		ms, _ := strconv.Atoi(m[2])
		sum += ms
		labels = append(labels, m[1])
	}
	if i+2 >= len(lines) || lines[i] != "  TOTAL: "+strconv.Itoa(sum)+"ms" {
		t.Fatalf("total line = %q after rows %v (sum %dms)", lines[min(i, len(lines)-1)], labels, sum)
	}
	if lines[i+1] != strings.Repeat("-", len(title)+8) || lines[i+2] != "" {
		t.Fatalf("closing = %q %q, want %d dashes and a blank line", lines[i+1], lines[i+2], len(title)+8)
	}
	// The main.ts marks in the order PiG's startup reaches them: resolveModelScope runs with the runtime build, before stdin is read.
	want := []string{"parseArgs", "runMigrations", "createSessionManager", "createRuntime", "createAgentSessionRuntime", "resolveModelScope", "readPipedStdin", "prepareInitialMessage", "initTheme", "createAgentSession"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Fatalf("marks = %v, want %v", labels, want)
	}
}

// timings.ts: ENABLED is `process.env.PI_TIMING === "1"`: unset, empty, or any other value prints nothing.
func TestPiTimingOnlyOneEnablesTheReport(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	for _, env := range []string{"", "0", "true", "yes"} {
		if stderr := runPigWithTiming(t, bin, env); strings.Contains(stderr, "Startup Timings") {
			t.Fatalf("PI_TIMING=%q printed a report: %q", env, stderr)
		}
	}
}
