package codingagent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// piSessionOrderDir holds entries.jsonl (the raw session lines, written with member orders, number forms and escapes a JavaScript object would not keep) and expected.json (what the real Pi 0.99.1 SessionManager, opened on that file, returns for each read, as JSON.stringify writes it). Pi 0.99.2 has the same session-manager.ts and messages.ts.
var piSessionOrderDir = filepath.Join("testdata", "pi-session-order")

func piSessionOrderView(t testing.TB) ExtensionSessionView {
	t.Helper()
	file, err := os.Open(filepath.Join(piSessionOrderDir, "entries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	session := NewSession("pi-order", "/w")
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		if err := session.AppendEntry(json.RawMessage(bytes.Clone(scanner.Bytes()))); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return ExtensionSessionView{Session: session, CWD: "/w"}
}

func piSessionOrderExpected(t testing.TB) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(piSessionOrderDir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]string
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	return expected
}

// Pi's SessionManager holds entries JSON.parse produced and builds its projections with object literals and spreads, so each read answers with the members in insertion order (integer-like keys first). The Go, Rust and Python SDKs ask the host for these reads: the host's answer must carry the same bytes, not the order of a Go map.
//
// upstream: packages/coding-agent/src/core/session-manager.ts:439-585 (sessionEntryToContextMessages, projectContextEntry, buildSessionProjection, buildSessionContext), 1529-1560 (getTree), core/messages.ts:100-137 (createBranchSummaryMessage, createCompactionSummaryMessage, createCustomMessage)
func TestExtensionSessionReadAnswersInPiMemberOrder(t *testing.T) {
	view := piSessionOrderView(t)
	expected := piSessionOrderExpected(t)
	for method, args := range map[string]string{
		"getEntries":             "",
		"getEntry":               `{"id":"a4"}`,
		"getLeafEntry":           "",
		"getBranch":              `{"fromId":"a4"}`,
		"getChildren":            `{"parentId":"a1"}`,
		"getTree":                "",
		"buildContextEntries":    "",
		"buildSessionProjection": "",
		"buildSessionContext":    "",
	} {
		t.Run(method, func(t *testing.T) {
			want, ok := expected[method]
			if !ok {
				t.Fatalf("no Pi expectation for %s", method)
			}
			var raw json.RawMessage
			if args != "" {
				raw = json.RawMessage(args)
			}
			got, err := ExtensionSessionRead(view, method, raw)
			if err != nil {
				t.Fatal(err)
			}
			// The wire carries JSON text: an HTML-escaped "<" decodes to the same string in every SDK, so the comparison writes without the escape and keeps everything a consumer can tell apart (member order, 1 against 1.0).
			var encoded bytes.Buffer
			encoder := json.NewEncoder(&encoded)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(got); err != nil {
				t.Fatal(err)
			}
			if text := string(bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))); text != want {
				t.Fatalf("%s\n got  %s\n want %s", method, text, want)
			}
		})
	}
}

// sessionEntryToContextMessages tests `entry.summary` for JavaScript truthiness, not for a non-empty string, so a hand-edited branch summary whose summary is a number still contributes a message (session-manager.ts:458-460). Expectation printed by the real Pi 0.99.1 SessionManager on these two lines.
func TestExtensionSessionReadBranchSummaryIsTruthy(t *testing.T) {
	session := NewSession("pi-truthy", "/w")
	for _, line := range []string{
		`{"type":"message","id":"b1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"x","timestamp":1}}`,
		`{"type":"branch_summary","id":"b2","parentId":"b1","timestamp":"2026-01-01T00:00:02.000Z","summary":5,"fromId":"b1"}`,
	} {
		if err := session.AppendEntry(json.RawMessage(line)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ExtensionSessionRead(ExtensionSessionView{Session: session, CWD: "/w"}, "buildSessionContext", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"messages":[{"role":"user","content":"x","timestamp":1},{"role":"branchSummary","summary":5,"fromId":"b1","timestamp":1767225602000}],"thinkingLevel":"off","model":null}`
	if string(encoded) != want {
		t.Fatalf("buildSessionContext\n got  %s\n want %s", encoded, want)
	}
}
