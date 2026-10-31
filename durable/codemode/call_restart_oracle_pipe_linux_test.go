package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// A user over the kernel's pipe-user-pages-soft limit gets a one-page pipe for every new pipe, and the oracle's report is larger
// than that: it must still arrive whole (the oracle ended with process.exit(0) right after process.stdout.write, which drops the part
// of the report the pipe had no room for, and the tests that read it failed with "unexpected end of JSON input").
func TestPisRestartOracleDeliversItsWholeReportThroughAOnePagePipe(t *testing.T) {
	const page = 4096
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	t.Cleanup(func() { _ = writer.Close() })
	raw, err := writer.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var sizeErr error
	if err := raw.Control(func(fd uintptr) { _, sizeErr = unix.FcntlInt(fd, unix.F_SETPIPE_SZ, page) }); err != nil || sizeErr != nil {
		t.Fatalf("cannot make the pipe one page: %v %v", err, sizeErr)
	}
	scenarios := []restartScenario{restartSteps("one call and a hold",
		`await tools.echo({ text: "a" }); await tools.hold({});`,
		[]restartStep{awaitJournal(1), awaitFirstIssued(1)}, nil)}
	// The report is what the oracle prints into a pipe with room for all of it.
	cmd, stderr := piRestartOracleCmd(t, scenarios)
	whole, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi's restart oracle: %v\n%s", err, stderr.String())
	}
	if len(whole) <= page {
		t.Fatalf("the report is %d bytes, which fits the pipe: the test no longer exercises a full pipe", len(whole))
	}
	cmd, stderr = piRestartOracleCmd(t, scenarios)
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The child holds the write end now; the parent's copy must go for the reader to see end of file.
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, readErr := io.ReadAll(reader)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Pi's restart oracle: %v\n%s", err, stderr.String())
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	var pi []json.RawMessage
	if err := json.Unmarshal(output, &pi); err != nil || len(output) != len(whole) {
		t.Fatalf("%d of the report's %d bytes arrived through the one-page pipe: %v", len(output), len(whole), err)
	}
}
