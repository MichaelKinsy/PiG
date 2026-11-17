package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi's 10470-rpc-session-replacement-start-once.test.ts (beffcde92dea, #12230): RPC mode runs the extensions' session_start once for a replacement
// Session, whatever command replaced it, and a client receives the events the session_start handlers emit, at startup and on every replacement.
// Pi runs the test on a real AgentSessionRuntime with an in-process extension; here pig runs as a process and the extension of
// testdata/session-start-once.mjs appends the same "start-marker" entry from session_start. The tests below are Pi's five cases with its inputs and its
// expectations: the markers `${reason}:${calls so far}` in the order of the calls, and the client's entry_appended events for them.

type startOnceRPC struct {
	t    *testing.T
	p    *rpcProcess
	log  string
	seen []rpcRecord
}

// startOnceRPCStart is Pi's startRpc: it starts RPC mode and returns once startup has written everything (a command's response follows the records startup wrote).
func startOnceRPCStart(t *testing.T) *startOnceRPC {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-start-once.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	c := &startOnceRPC{t: t, log: filepath.Join(t.TempDir(), "starts.log")}
	c.p = startRPCProcessAt(t, t.TempDir(), []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_TEST_START_LOG=" + c.log,
	}, "--model", "test-faux/faux-1", "-e", fixture)
	c.send(map[string]any{"type": "get_state"})
	return c
}

// send is Pi's send: it sends the command with an id, waits for its response and expects success. Every record read on the way is kept.
func (c *startOnceRPC) send(command map[string]any) rpcRecord {
	c.t.Helper()
	id := fmt.Sprintf("req-%d", len(c.seen))
	command["id"] = id
	c.p.sendJSON(command)
	var response rpcRecord
	c.p.await("the response to "+id, func(record rpcRecord) bool {
		c.seen = append(c.seen, record)
		if record["type"] == "response" && record["id"] == id {
			response = record
			return true
		}
		return false
	})
	if response["success"] != true {
		c.t.Fatalf("%v: %v\n%s", command, response, c.p.stderr.String())
	}
	return response
}

// starts is Pi's `starts` array: the marker every session_start call recorded, in order.
func (c *startOnceRPC) starts() []string {
	c.t.Helper()
	data, err := os.ReadFile(c.log)
	if err != nil {
		c.t.Fatal(err)
	}
	return slices.DeleteFunc(strings.Split(string(data), "\n"), func(line string) bool { return line == "" })
}

// streamedStartEntries is Pi's streamedStartEntries: the data of every entry_appended event for the custom entries the extension writes in session_start.
func (c *startOnceRPC) streamedStartEntries() []string {
	entries := []string{}
	for _, record := range c.seen {
		entry, _ := record["entry"].(map[string]any)
		if record["type"] == "entry_appended" && entry["type"] == "custom" && entry["customType"] == "start-marker" {
			data, _ := entry["data"].(string)
			entries = append(entries, data)
		}
	}
	return entries
}

// promptOnce runs one prompt so the session has a user message and is written to disk; it returns that message's entry id.
func (c *startOnceRPC) promptOnce() string {
	c.t.Helper()
	c.send(map[string]any{"type": "prompt", "message": "reply with exactly: first"})
	c.p.await("agent_settled", func(record rpcRecord) bool {
		c.seen = append(c.seen, record)
		return record["type"] == "agent_settled"
	})
	messages, _ := c.send(map[string]any{"type": "get_fork_messages"})["data"].(map[string]any)["messages"].([]any)
	if len(messages) == 0 {
		c.t.Fatal("the session has no user message to fork from")
	}
	entryID, _ := messages[0].(map[string]any)["entryId"].(string)
	return entryID
}

// sessionFile is the current session's file, which a Session is resumed from.
func (c *startOnceRPC) sessionFile() string {
	c.t.Helper()
	file, _ := c.send(map[string]any{"type": "get_state"})["data"].(map[string]any)["sessionFile"].(string)
	if file == "" {
		c.t.Fatal("the session has no file")
	}
	return file
}

// expectOneStart is Pi's expectOneStart: the replacing command ran session_start once and streamed the entry that handler appended.
func (c *startOnceRPC) expectOneStart(command map[string]any, reason string) {
	c.t.Helper()
	before := len(c.starts())
	c.send(command)
	marker := fmt.Sprintf("%s:%d", reason, before)
	if got := c.starts()[before:]; !slices.Equal(got, []string{marker}) {
		c.t.Fatalf("%v ran session_start %q, want [%q]", command["type"], got, marker)
	}
	var streamed []string
	for _, entry := range c.streamedStartEntries() {
		if entry == marker {
			streamed = append(streamed, entry)
		}
	}
	if !slices.Equal(streamed, []string{marker}) {
		c.t.Fatalf("%v streamed %q for the entry its session_start appended, want [%q]\n%s", command["type"], streamed, marker, c.p.stderr.String())
	}
}

func TestRPCStreamsEntriesAppendedByTheStartupSessionStartUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	c := startOnceRPCStart(t)
	if got := c.starts(); !slices.Equal(got, []string{"startup:0"}) {
		t.Fatalf("starts = %q, want [startup:0]", got)
	}
	if got := c.streamedStartEntries(); !slices.Equal(got, []string{"startup:0"}) {
		t.Fatalf("streamed start entries = %q, want [startup:0]", got)
	}
}

func TestRPCNewSessionRunsSessionStartOnceUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	c := startOnceRPCStart(t)
	c.expectOneStart(map[string]any{"type": "new_session"}, "new")
}

func TestRPCSwitchSessionRunsSessionStartOnceUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	c := startOnceRPCStart(t)
	c.promptOnce()
	sessionPath := c.sessionFile()
	c.send(map[string]any{"type": "new_session"})
	c.expectOneStart(map[string]any{"type": "switch_session", "sessionPath": sessionPath}, "resume")
}

func TestRPCForkRunsSessionStartOnceUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	c := startOnceRPCStart(t)
	entryID := c.promptOnce()
	c.expectOneStart(map[string]any{"type": "fork", "entryId": entryID}, "fork")
}

func TestRPCCloneRunsSessionStartOnceUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	c := startOnceRPCStart(t)
	c.promptOnce()
	c.expectOneStart(map[string]any{"type": "clone"}, "fork")
}
