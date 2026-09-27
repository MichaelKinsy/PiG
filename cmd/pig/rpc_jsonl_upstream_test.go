package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/rpcclient"
)

func TestRPCJSONLUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/rpc-jsonl.test.ts:6
	t.Run("serializes strict JSONL records without escaping Unicode separators", func(t *testing.T) {
		for _, text := range []string{"a\u2028b\u2029c", `\u2028\u2029`, "\\\u2028"} {
			var output bytes.Buffer
			writeJSONLine(&output, map[string]string{"text": text})
			if !strings.Contains(output.String(), strings.ReplaceAll(text, `\`, `\\`)) || !strings.HasSuffix(output.String(), "\n") {
				t.Fatalf("JSONL = %q", output.String())
			}
			var decoded map[string]string
			if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded["text"] != text {
				t.Fatalf("roundtrip = %v, %v", decoded, err)
			}
		}
	})
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/rpc-jsonl.test.ts:14
		{"splits on LF only and preserves U+2028/U+2029 inside payloads", "{\"text\":\"a\u2028b\u2029c\"}\n", []string{"{\"text\":\"a\u2028b\u2029c\"}"}},
		// .upstream/v0.87.1/packages/coding-agent/test/rpc-jsonl.test.ts:33
		{"handles CRLF-delimited input", "{\"a\":1}\r\n{\"b\":2}\r\n", []string{`{"a":1}`, `{"b":2}`}},
		// .upstream/v0.87.1/packages/coding-agent/test/rpc-jsonl.test.ts:51
		{"emits a final line without trailing LF", `{"a":1}`, []string{`{"a":1}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lines []string
			if err := rpcclient.ReadJSONLLines(strings.NewReader(tc.input), func(line []byte) bool { lines = append(lines, string(line)); return true }); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(lines, tc.want) {
				t.Fatalf("lines = %q, want %q", lines, tc.want)
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5868-rpc-unknown-command-id.test.ts:93
func TestRPCUnknownCommandPreservesRequestID(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent")}, "--no-extensions", "--offline", "--no-session")
	p.send(`{"id":"test","type":"foobar"}`)
	p.await("unknown command response", func(record rpcRecord) bool {
		if record["type"] != "response" {
			return false
		}
		want := rpcRecord{"id": "test", "type": "response", "command": "foobar", "success": false, "error": "Unknown command: foobar"}
		if !reflect.DeepEqual(record, want) {
			t.Fatalf("response = %#v, want %#v", record, want)
		}
		return true
	})
	// Also drive unterminated framing through the real command reader.
	if _, err := io.WriteString(p.stdin, `{"id":"last","type":"foobar"}`); err != nil {
		t.Fatal(err)
	}
	p.closeInput()
	p.await("final unterminated command", func(record rpcRecord) bool { return record["id"] == "last" })
	p.waitForExit("after final record")
}
