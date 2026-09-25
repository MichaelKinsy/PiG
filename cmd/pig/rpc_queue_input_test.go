package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// TestRPCSteerAndFollowUpRunInputHandlers pins upstream rpc-mode steer and
// follow_up, which call session.steer/followUp and so _queueUserInput: the
// extension input handlers run (source "rpc", no streaming behavior while
// idle) before the message is queued, and a handled message is not queued.
// Pig queued steer and follow_up text directly.
func TestRPCSteerAndFollowUpRunInputHandlers(t *testing.T) {
	p := startRPCUIFixture(t, []string{"PIG_HOME=" + t.TempDir()}, "--model", "test-faux/echo", "--no-session")
	for _, command := range []string{"steer", "follow_up"} {
		id := "queue-" + command
		p.send(fmt.Sprintf(`{"id":%q,"type":%q,"message":"handled input"}`, id, command))
		gotNotify := false
		p.awaitProgress(func() string { return fmt.Sprintf("%s input handler: notify=%v", command, gotNotify) }, func(record rpcRecord) bool {
			if record["type"] == "extension_ui_request" && record["method"] == "notify" && record["message"] == "input:rpc:idle" {
				gotNotify = true
			}
			if record["type"] == "response" && record["id"] == id {
				if record["success"] != true || !gotNotify {
					t.Fatalf("%s response %v before the input handler ran (notify=%v)", command, record, gotNotify)
				}
				return true
			}
			return false
		})
	}
	p.send(`{"id":"state","type":"get_state"}`)
	p.await("state after handled queue input", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		if data["pendingMessageCount"] != float64(0) {
			t.Fatalf("pendingMessageCount = %v, want 0: a handled message was queued", data["pendingMessageCount"])
		}
		return true
	})
	p.closeAndWait("after handled queue input")
}

// TestRPCReadsCommandLinesWithoutALengthCap pins upstream jsonl.ts
// attachJsonlLineReader, which buffers until "\n" with no line cap. Pig's
// scanner stopped at 16 MiB and ended the session with a read error, so a
// prompt carrying a large attachment could not be sent. GUARD-04.
func TestRPCReadsCommandLinesWithoutALengthCap(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcess(t, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + home + "/agent"}, "--no-session")
	p.send(`{"id":"big","type":"get_state","padding":"` + strings.Repeat("x", 17<<20) + `"}`)
	p.await("response to a 17 MiB command line", func(record rpcRecord) bool {
		if record["type"] == "error" {
			t.Fatalf("RPC read error for a long line: %v", record)
		}
		return isSuccessResponse(record, "big")
	})
	p.closeAndWait("after a 17 MiB command line")
}

// TestRPCReadsSplitInvalidBlankAndFinalUnterminatedRecords drives the production
// RPC parser with the split and malformed framing accepted by upstream jsonl.ts.
func TestRPCReadsSplitInvalidBlankAndFinalUnterminatedRecords(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcess(t, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + home + "/agent"}, "--no-session")
	for _, chunk := range []string{
		`{"id":"split","type":"get_`,
		"state\"}\n",
		"not json\n",
		"\n",
		"\r",
	} {
		if _, err := io.WriteString(p.stdin, chunk); err != nil {
			t.Fatal(err)
		}
	}
	p.closeInput()

	gotSplit := false
	parseErrors := 0
	p.awaitProgress(func() string {
		return fmt.Sprintf("split response and three parse errors (split=%v parse=%d)", gotSplit, parseErrors)
	}, func(record rpcRecord) bool {
		if isSuccessResponse(record, "split") {
			gotSplit = true
		}
		if record["type"] == "response" && record["command"] == "parse" && record["success"] == false {
			parseErrors++
		}
		return gotSplit && parseErrors == 3
	})
	p.waitForExit("after split, invalid, blank, and final unterminated records")
}

// Upstream rpc-mode has no prompt metadata: it writes no side files and sets
// no environment for a prompt. Pig wrote the prompt's task id and metadata
// to world-readable /tmp files, discarding every write error. GUARD-19.
func TestRPCPromptWritesNoMetadataSideFiles(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + home + "/agent",
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
	}, "--model", "test-faux/faux-1", "--no-session")
	p.send(`{"id":"meta","type":"prompt","message":"reply with exactly: meta","metadata":{"task_id":"task-guard-19"}}`)
	p.await("the metadata prompt settling", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	pid := p.cmd.Process.Pid
	for _, path := range []string{
		fmt.Sprintf("/tmp/.pig-current-task-id.%d", pid),
		fmt.Sprintf("/tmp/.pig-current-prompt-metadata.%d", pid),
	} {
		if _, err := os.Stat(path); err == nil {
			_ = os.Remove(path)
			t.Errorf("RPC prompt wrote side file %s", path)
		}
	}
	p.closeAndWait("after the metadata prompt")
}
