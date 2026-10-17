package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// The context of a file whose messages Pi did not write keeps each message's object as JSON.parse returned it: a context edit replaces content in
// its place ({ ...message, content }, session-manager.ts applyContextEditReplacement), a string replacement of an assistant becomes one text
// block, a message without content gets content: [] after its other members, and a system message without content gets content: ""
// (sessionEntryToContextMessages); a context edit leaves a system message as it is (projectContextEntry). The expected lines are Pi
// 1.1.0's JSON.stringify of buildSessionContext(entries).messages for these entries.
func TestSessionContextKeepsTheMemberOrderOfMessagesPiDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"session","version":3,"id":"0199aaaa-0000-7000-8000-000000000001","timestamp":"2026-10-10T00:00:00.000Z","cwd":"` + filepath.ToSlash(dir) + `"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-10T00:00:01.000Z","message":{"timestamp":1,"content":"old","x":{"b":1,"a":2},"role":"user"}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-10T00:00:02.000Z","message":{"content":[{"type":"text","text":"old"}],"role":"assistant","api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2,"zz":true}}`,
		`{"type":"message","id":"u2","parentId":"a1","timestamp":"2026-10-10T00:00:03.000Z","message":{"role":"user","timestamp":3}}`,
		`{"type":"message","id":"s1","parentId":"u2","timestamp":"2026-10-10T00:00:03.100Z","message":{"timestamp":4,"content":"sys","role":"system","zz":1}}`,
		`{"type":"message","id":"s2","parentId":"s1","timestamp":"2026-10-10T00:00:03.200Z","message":{"role":"system","timestamp":5}}`,
		`{"type":"message","id":"s3","parentId":"s2","timestamp":"2026-10-10T00:00:03.300Z","message":{"role":"system","content":null,"timestamp":6}}`,
		`{"type":"context_edit","id":"e1","parentId":"s3","timestamp":"2026-10-10T00:00:04.000Z","targetId":"u1","replacement":{"content":"new user"}}`,
		`{"type":"context_edit","id":"e2","parentId":"e1","timestamp":"2026-10-10T00:00:05.000Z","targetId":"a1","replacement":{"content":"imported string"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"timestamp":1,"content":"new user","x":{"b":1,"a":2},"role":"user"}`,
		`{"content":[{"type":"text","text":"imported string"}],"role":"assistant","api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2,"zz":true}`,
		`{"role":"user","timestamp":3,"content":[]}`,
		`{"timestamp":4,"content":"sys","role":"system","zz":1}`,
		`{"role":"system","timestamp":5,"content":""}`,
		`{"role":"system","content":"","timestamp":6}`,
	}
	messages := session.BuildSessionContext().Messages
	if len(messages) != len(want) {
		t.Fatalf("%d messages, want %d", len(messages), len(want))
	}
	for i, message := range messages {
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		got, err := jsonstringify.Canonicalize(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want[i] {
			t.Errorf("message %d:\n got %s\nwant %s", i, got, want[i])
		}
	}
}
