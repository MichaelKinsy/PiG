package sdk

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSessionEntriesSkipsHeaderAndPreservesOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := "" +
		`{"type":"session","id":"session-id"}` + "\n" +
		`{"type":"message","id":"first"}` + "\n" +
		`{"type":"message","id":"second","parentId":"first"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := readSessionEntries(path)
	if len(entries) != 2 || string(entries[0]) != `{"type":"message","id":"first"}` || string(entries[1]) != `{"type":"message","id":"second","parentId":"first"}` {
		t.Fatalf("entries = %q", entries)
	}
}

func TestMalformedSessionFileFallsBackToHostFromCursorZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session\"}\n{"), 0o600); err != nil {
		t.Fatal(err)
	}

	clientNet, hostNet := net.Pipe()
	client := newConn(clientNet)
	client.start()
	t.Cleanup(func() {
		_ = clientNet.Close()
		_ = hostNet.Close()
	})

	hostDone := make(chan error, 1)
	go func() {
		host := newConn(hostNet)
		for requestNumber := range 2 {
			data, err := host.readFrame()
			if err != nil {
				hostDone <- err
				return
			}
			var request envelope
			if err := json.Unmarshal(data, &request); err != nil {
				hostDone <- err
				return
			}
			if request.Type != msgCall || request.Call == nil || request.Call.Method != "watchSessionLog" {
				hostDone <- fmt.Errorf("request %d = %#v", requestNumber, request)
				return
			}
			var args struct {
				Cursor   int  `json:"cursor"`
				Complete bool `json:"complete"`
			}
			if err := json.Unmarshal(request.Call.Args, &args); err != nil {
				hostDone <- err
				return
			}
			if requestNumber == 0 && (args.Cursor != 0 || args.Complete) {
				hostDone <- fmt.Errorf("initial args = %+v", args)
				return
			}
			if requestNumber == 1 && (args.Cursor != 1 || !args.Complete) {
				hostDone <- fmt.Errorf("completion args = %+v", args)
				return
			}
			result := map[string]any{"entryCount": 1, "hasMore": false, "leafId": "host-entry"}
			if requestNumber == 0 {
				result["entries"] = []json.RawMessage{json.RawMessage(`{"type":"message","id":"host-entry"}`)}
			}
			resultJSON, err := json.Marshal(result)
			if err != nil {
				hostDone <- err
				return
			}
			responseJSON, err := json.Marshal(envelope{Type: msgCallResult, ID: request.ID, CallResult: &callResultMsg{Result: resultJSON}})
			if err != nil {
				hostDone <- err
				return
			}
			if err := host.writeFrame(responseJSON); err != nil {
				hostDone <- err
				return
			}
		}
		hostDone <- nil
	}()

	ext := New("malformed-session")
	ext.conn = client
	ext.sessionFile = path
	ext.ensureSessionLog()
	entries := ext.session.getEntries()
	if len(entries) != 1 || string(entries[0]) != `{"type":"message","id":"host-entry"}` {
		t.Fatalf("entries = %q", entries)
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
}

func TestReadSessionEntriesRejectsPartialLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"message\"}\n{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries := readSessionEntries(path); entries != nil {
		t.Fatalf("partial log returned %d entries", len(entries))
	}
}
