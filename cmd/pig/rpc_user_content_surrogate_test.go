package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestRPCPersistedUserContentPreservesSurrogateEscapes(t *testing.T) {
	// Review CLIEXT-013: expected content comes from independent raw fixtures, never from a lossy Session decode.
	contents := []string{`"a\ud800b"`, `"a\udfffb"`, `"😀\ud800é"`, `"a�b"`, `[{"type":"text","text":"a\ud800b"}]`}
	home := t.TempDir()
	path := filepath.Join(home, "history.jsonl")
	header, err := json.Marshal(codingagent.SessionHeader{Type: "session", Version: codingagent.CurrentSessionVersion, ID: "01900000-0000-7000-8000-000000000001", Timestamp: "2026-07-01T00:00:00.000Z", CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	log.Write(header)
	log.WriteByte('\n')
	for i, content := range contents {
		parent := "null"
		if i > 0 {
			parent = fmt.Sprintf(`"%08x"`, i)
		}
		fmt.Fprintf(&log, `{"type":"message","id":"%08x","parentId":%s,"timestamp":"2026-07-01T00:00:00.000Z","message":{"role":"user","content":%s,"timestamp":%d}}`+"\n", i+1, parent, content, i+1)
	}
	if err := os.WriteFile(path, log.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := codingagent.NewSessionManagerWithDir(home, home)
	session, err := manager.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	messages := session.BuildContext(session.LeafID())
	if len(messages) != len(contents) {
		t.Fatalf("loaded %d messages, want %d", len(messages), len(contents))
	}
	// Force the decoded values back through the production persistence writer too.
	for _, message := range messages {
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}

	binary := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(binary, "--offline", "--mode", "rpc", "--model", "test-faux/echo", "--session", path, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files")
	cmd.Env = append(os.Environ(), "PIG_TEST_FAUX=1", "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"))
	cmd.Stdin = strings.NewReader("{\"id\":\"history\",\"type\":\"get_messages\"}\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC: %v\n%s", err, stderr.String())
	}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		var response struct {
			ID      string
			Success bool
			Data    struct {
				Messages []struct {
					Role    string
					Content json.RawMessage
				}
			}
		}
		if line == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.ID != "history" {
			continue
		}
		if !response.Success || len(response.Data.Messages) != 2*len(contents) {
			t.Fatalf("response=%s", line)
		}
		for i, message := range response.Data.Messages {
			if want := contents[i%len(contents)]; message.Role != "user" || string(message.Content) != want {
				t.Errorf("message %d content=%s, want %s", i, message.Content, want)
			}
		}
		return
	}
	t.Fatalf("no history response: %s\n%s", stdout.String(), stderr.String())
}
