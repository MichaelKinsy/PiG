package codingagent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi's loader reads each session line with JSON.parse (session-manager.ts:616-624), so an integer member written as an exponent or a whole fraction is that number, and buildSessionContext keeps the message. The expected text is Pi 1.1.0's JSON.stringify(SessionManager.open(file).buildSessionContext().messages) for this file.
func TestSessionContextKeepsMessagesWhoseIntegersAreWrittenAsExponentOrWholeFraction(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + quoteJSON(t, dir) + `}`,
		`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1.767225603e12}}`,
		`{"type":"message","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"t","arguments":{}}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1e2,"output":5.0,"cacheRead":0,"cacheWrite":0,"totalTokens":1.05e2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1767225604000.0,"durationMs":2.5e1}}`,
		`{"type":"message","id":"a3","parentId":"a2","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"t","content":[{"type":"text","text":"ok"}],"isError":false,"durationMs":7E0,"timestamp":1.767225605e12}}`,
	}
	path := fileOperationWrite(t, dir, "spelling.jsonl", strings.Join(lines, "\n")+"\n")
	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(session.BuildSessionContext().Messages)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"role":"user","content":"hi","timestamp":1767225603000},{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"t","arguments":{}}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":100,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":105,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1767225604000,"durationMs":25},{"role":"toolResult","toolCallId":"c1","toolName":"t","content":[{"type":"text","text":"ok"}],"isError":false,"durationMs":7,"timestamp":1767225605000}]`
	if string(got) != want {
		t.Fatalf("session context\n got %s\nwant %s", got, want)
	}
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
