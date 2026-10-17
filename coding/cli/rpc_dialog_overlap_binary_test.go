package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const rpcDialogOverlapExtension = `export default function (pi) {
  pi.registerCommand("probe", { handler: async (a, ctx) => {
    const both = await Promise.all([ctx.ui.select("Pick", ["a1"]), ctx.ui.confirm("Sure", "really?")]);
    ctx.ui.notify("both=" + JSON.stringify(both), "info");
    const pending = ctx.ui.select("Later", ["b1"]);
    const custom = await ctx.ui.custom(() => ({ render: () => [], invalidate() {} }));
    ctx.ui.notify("custom=" + String(custom), "info");
    ctx.ui.notify("later=" + String(await pending), "info");
  }});
}
`

// Pi 1.1.0's RPC mode writes each dialog request when the extension calls it (rpc-mode.ts createDialogPromise), so overlapping dialogs are all pending at once and custom UI resolves while a dialog waits. PiG queued RPC dialogs behind the TUI focus: the confirm request appeared only after the select was answered, and custom waited for the pending select. The client here answers only what Pi has already sent, so the old order never completes.
func TestRPCOverlappingDialogsAreAllPendingAtOnceAsPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	extPath := filepath.Join(dir, "probe.mjs")
	if err := os.WriteFile(extPath, []byte(rpcDialogOverlapExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	cmd := exec.Command(bin, "--mode", "rpc", "--no-session", "--no-extensions", "--offline", "-e", extPath, "--model", "test-faux/faux-1")
	cmd.Dir = agentDir
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	type request struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	var mu sync.Mutex
	var transcript strings.Builder
	records := make(chan request, 64)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(records)
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(nil, 16<<20)
		for scanner.Scan() {
			mu.Lock()
			transcript.WriteString(scanner.Text() + "\n")
			mu.Unlock()
			var record request
			if json.Unmarshal(scanner.Bytes(), &record) == nil {
				records <- record
			}
		}
	}()
	deadline := time.After(testbudget.Wait(t))
	fail := func(format string, args ...any) {
		t.Helper()
		_ = cmd.Process.Kill()
		<-readDone
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf(format+"\nstdout:\n%s\nstderr:\n%s", append(args, transcript.String(), stderr.String())...)
	}
	next := func() request {
		t.Helper()
		select {
		case record, ok := <-records:
			if !ok {
				fail("pig exited early")
			}
			return record
		case <-deadline:
			fail("timed out")
		}
		return request{}
	}
	answer := func(id string, fields string) {
		t.Helper()
		if _, err := fmt.Fprintf(stdin, `{"type":"extension_ui_response","id":%q,%s}`+"\n", id, fields); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.WriteString(stdin, `{"id":"c","type":"prompt","message":"/probe"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	dialogs := map[string]string{}
	var notifies []string
	for done := false; !done; {
		record := next()
		switch {
		case record.Type == "extension_ui_request" && record.Method == "notify":
			notifies = append(notifies, record.Message)
			if record.Message == "custom=undefined" {
				answer(dialogs["Later"], `"value":"b1"`)
			}
		case record.Type == "extension_ui_request":
			dialogs[record.Title] = record.ID
			if dialogs["Pick"] != "" && dialogs["Sure"] != "" && record.Title != "Later" {
				answer(dialogs["Pick"], `"value":"a1"`)
				answer(dialogs["Sure"], `"confirmed":true`)
			}
		case record.Type == "response" && record.ID == "c":
			done = true
		}
	}
	_ = stdin.Close()
	<-readDone
	if err := cmd.Wait(); err != nil {
		t.Fatalf("pig --mode rpc: %v\nstderr: %s", err, stderr.String())
	}
	want := []string{`both=["a1",true]`, "custom=undefined", "later=b1"}
	if strings.Join(notifies, "\n") != strings.Join(want, "\n") {
		t.Errorf("notify messages = %q, want %q", notifies, want)
	}
}
