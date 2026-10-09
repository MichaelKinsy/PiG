package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

var rpcIngressUTF16Cases = []struct {
	name, wire, want string
	units            []uint16
}{
	{"empty", `""`, `""`, nil},
	{"ordinary", `"hello"`, `"hello"`, []uint16{'h', 'e', 'l', 'l', 'o'}},
	{"high", `"a\ud800b"`, `"a\ud800b"`, []uint16{'a', 0xd800, 'b'}},
	{"low", `"a\udfffb"`, `"a\udfffb"`, []uint16{'a', 0xdfff, 'b'}},
	{"pair", `"\ud83d\ude00"`, `"😀"`, []uint16{0xd83d, 0xde00}},
	{"replacement", `"a�b"`, `"a�b"`, []uint16{'a', 0xfffd, 'b'}},
	{"literal escape", `"\\ud800"`, `"\\ud800"`, []uint16{'\\', 'u', 'd', '8', '0', '0'}},
	{"mixed", `"😀\ud800é"`, `"😀\ud800é"`, []uint16{0xd83d, 0xde00, 0xd800, 0x00e9}},
}

func TestRPCCommandEnvelopePreservesUTF16AndIDPresence(t *testing.T) {
	for _, tc := range rpcIngressUTF16Cases {
		t.Run(tc.name, func(t *testing.T) {
			line := []byte(`{"type":"steer","id":` + tc.wire + `,"message":` + tc.wire + `}`)
			envelope, err := parseRPCCommand(line)
			if err != nil {
				t.Fatal(err)
			}
			var id string
			if envelope.ID == nil || sdkjson.Unmarshal(envelope.ID, &id) != nil {
				t.Fatalf("explicit request ID = %s, want a string", envelope.ID)
			}
			if got := jsstring.ToUTF16(id); !slices.Equal(got, tc.units) {
				t.Fatalf("id UTF-16=%x, want %x", got, tc.units)
			}
			if string(envelope.Raw) != string(line) {
				t.Fatal("command envelope changed raw command bytes")
			}
		})
	}
	envelope, err := parseRPCCommand([]byte(`{"type":"get_state"}`))
	if err != nil || envelope.ID != nil {
		t.Fatalf("omitted id=%s err=%v", envelope.ID, err)
	}
}

// Each command is acknowledged before the next is sent. This proves ingress and retained queue state without relying on same-batch RPC Promise scheduling or a Provider request.
func TestRPCFreshQueueTextPreservesUTF16(t *testing.T) {
	client := newRPCIngressWireClient(t)
	for _, command := range []string{"steer", "follow_up"} {
		for _, tc := range rpcIngressUTF16Cases {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				client.t = t
				client.send(`{"type":"` + command + `","id":"queued","message":` + tc.wire + `}`)
				client.response(command, `"queued"`)
				client.send(`{"type":"clear_queue","id":"clear"}`)
				response := client.response("clear_queue", `"clear"`)
				var data struct {
					Steering json.RawMessage `json:"steering"`
					FollowUp json.RawMessage `json:"followUp"`
				}
				if err := json.Unmarshal(response["data"], &data); err != nil {
					t.Fatal(err)
				}
				got, other := data.Steering, data.FollowUp
				if command == "follow_up" {
					got, other = data.FollowUp, data.Steering
				}
				if string(got) != "["+tc.want+"]" || string(other) != "[]" {
					t.Errorf("queue=%s other=%s, want [%s] and []", got, other, tc.want)
				}
			})
		}
	}
}

func TestRPCFreshPromptTextPreservesUTF16(t *testing.T) {
	client := newRPCIngressWireClient(t)
	for _, tc := range rpcIngressUTF16Cases {
		t.Run(tc.name, func(t *testing.T) {
			client.t = t
			// The fixed prefix keeps every prompt nonempty; the empty-string boundary is tested by queue and envelope cases.
			client.send(`{"type":"prompt","id":"prompt","message":"prompt:` + tc.wire[1:] + `}`)
			client.response("prompt", `"prompt"`)
			for record := client.next(); string(record["type"]) != `"agent_settled"`; record = client.next() {
			}
			client.send(`{"type":"get_messages","id":"history"}`)
			response := client.response("get_messages", `"history"`)
			var data struct {
				Messages []struct {
					Role    string
					Content json.RawMessage
				}
			}
			if err := json.Unmarshal(response["data"], &data); err != nil {
				t.Fatal(err)
			}
			var content json.RawMessage
			for _, message := range data.Messages {
				if message.Role == "user" {
					content = message.Content
				}
			}
			want := `[{"type":"text","text":"prompt:` + tc.want[1:] + `}]`
			if string(content) != want {
				t.Errorf("fresh user content=%s, want %s", content, want)
			}
		})
	}
}

func TestRPCFreshRequestIDPreservesUTF16(t *testing.T) {
	client := newRPCIngressWireClient(t)
	client.send(`{"type":"get_state"}`)
	client.response("get_state", "")
	for _, tc := range rpcIngressUTF16Cases {
		t.Run(tc.name, func(t *testing.T) {
			client.t = t
			client.send(`{"type":"get_state","id":` + tc.wire + `}`)
			client.response("get_state", tc.want)
		})
	}
}

// Capture fields as raw JSON. Decoding them into Go strings with encoding/json would replace the very units these assertions must distinguish.
type rpcIngressWireClient struct {
	t       *testing.T
	stdin   io.WriteCloser
	decoder *json.Decoder
	stderr  *lockedBuffer
}

func newRPCIngressWireClient(t *testing.T) *rpcIngressWireClient {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	// testing cancels t.Context before cleanup; the fixture owns cancellation until it has closed stdin and joined the child's graceful exit.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), testbudget.Wait(t))
	cmd := exec.CommandContext(ctx, binary, "--offline", "--no-session", "--mode", "rpc", "--model", "test-faux/echo", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+filepath.Join(root, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(root, "agent"), "PIG_TEST_FAUX=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			cancel()
		}
		_ = stdin.Close()
		err := cmd.Wait()
		cancel()
		if err != nil && !t.Failed() {
			t.Errorf("RPC exit: %v\n%s", err, stderr.String())
		}
	})
	return &rpcIngressWireClient{t: t, stdin: stdin, decoder: json.NewDecoder(stdout), stderr: stderr}
}

func (client *rpcIngressWireClient) send(line string) {
	client.t.Helper()
	if _, err := fmt.Fprintln(client.stdin, line); err != nil {
		client.t.Fatal(err)
	}
}

func (client *rpcIngressWireClient) next() map[string]json.RawMessage {
	client.t.Helper()
	var record map[string]json.RawMessage
	if err := client.decoder.Decode(&record); err != nil {
		client.t.Fatalf("RPC record: %v\n%s", err, client.stderr.String())
	}
	return record
}

func (client *rpcIngressWireClient) response(command, id string) map[string]json.RawMessage {
	client.t.Helper()
	for {
		record := client.next()
		if string(record["type"]) != `"response"` || string(record["command"]) != `"`+command+`"` {
			continue
		}
		if string(record["success"]) != "true" {
			client.t.Fatalf("RPC %s failed: %s", command, record["error"])
		}
		if string(record["id"]) != id {
			client.t.Errorf("response id=%s, want %s", record["id"], id)
		}
		return record
	}
}
