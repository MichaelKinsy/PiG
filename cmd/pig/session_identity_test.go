package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"

	"github.com/google/uuid"
)

// Pi's SessionManager.newSession creates the header/id before choosing a file;
// inMemory changes persistence, not identity (session-manager.ts:1057-1087,1801-1802).
func TestSessionReadBindingsKeepIdentityWithoutPersistence(t *testing.T) {
	for _, noSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "memory"}[noSession], func(t *testing.T) {
			services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			var current *coding.Session
			dir := t.TempDir()
			bridge := subprocess.NewUIBridge(func() {})
			bindSessionReadActions(bridge, func() *coding.Session { return current }, services.CWD(), dir)
			var previous string
			for range 2 {
				current, err = coding.NewSession(services, coding.SessionOptions{NoSession: noSession, SessionDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				session := current
				t.Cleanup(func() { _ = session.Close() })
				id := current.ID()
				requireSessionUUIDv7(t, id)
				if id == previous || current.Inner().Header().ID != id {
					t.Fatalf("session/header identity = %q, previous %q", id, previous)
				}
				previous = id
				if state := bridge.Snapshot(nil, 0, false); state.Session == nil || state.Session.SessionID != id || state.Session.SessionFile != current.Path() {
					t.Fatalf("Node state = %+v, want %s", state.Session, id)
				}
				for method, args := range map[string]json.RawMessage{
					"getSessionID": nil,
					"sessionRead":  json.RawMessage(`{"method":"getSessionId"}`),
				} {
					result, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: method, Args: args})
					if err != nil || result.Error != nil || !strings.Contains(string(result.Result), `"`+id+`"`) {
						t.Fatalf("%s = %+v, %v, want %s", method, result, err, id)
					}
				}
				if noSession && current.Path() != "" {
					t.Fatalf("in-memory session has path %q", current.Path())
				}
				if err := current.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func requireSessionUUIDv7(t *testing.T, id string) {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 || parsed.String() != id {
		t.Fatalf("session id %q is not a canonical UUIDv7: %v", id, err)
	}
}

type sessionIdentityRecord struct {
	Event     string `json:"event"`
	ID        string `json:"id"`
	Persisted bool   `json:"persisted"`
	File      string `json:"file"`
	Header    struct {
		ID string `json:"id"`
	} `json:"header"`
}

// The real CLI must bind the current SessionManager before dispatching lifecycle
// handlers and tools in print/JSON/RPC, including --no-session (Pi main.ts:363-447).
func TestHeadlessExtensionSessionIdentity(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs("../../test/parity/scenarios/testdata/extension-session-id.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"print", "json", "rpc"} {
		for _, noSession := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "persisted", true: "memory"}[noSession], func(t *testing.T) {
				home, cwd, dir := t.TempDir(), t.TempDir(), t.TempDir()
				var previous string
				for range 2 {
					report, raw := filepath.Join(t.TempDir(), "report.jsonl"), filepath.Join(t.TempDir(), "raw.jsonl")
					env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "pig"), "PI_CODING_AGENT_DIR=" + filepath.Join(home, "pi"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "SESSION_ID_REPORT=" + report, "SESSION_ID_RAW=" + raw}
					args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--model", "test-faux/faux-1", "--session-dir", dir, "-e", fixture}
					if noSession {
						args = append(args, "--no-session")
					}
					if mode == "rpc" {
						process := startRPCProcessAt(t, cwd, env, args...)
						process.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "Run: extension echo hello"})
						process.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
						process.sendJSON(map[string]any{"id": "settled", "type": "get_state"})
						process.await("settled", func(r rpcRecord) bool { return isSuccessResponse(r, "settled") })
						process.closeAndWait("session identity")
						if text := process.stderr.String(); text != "" {
							t.Fatalf("RPC stderr: %s", text)
						}
					} else {
						if mode == "print" {
							args = append(args, "--print")
						} else {
							args = append(args, "--mode", "json")
						}
						cmd := exec.CommandContext(t.Context(), binary, append(args, "Run: extension echo hello")...)
						cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
						out, err := cmd.CombinedOutput()
						if err != nil || !strings.Contains(string(out), "echo-bridge: hello") || strings.Contains(string(out), "Extension error") {
							t.Fatalf("%s: %v\n%s", mode, err, out)
						}
					}
					data, err := os.ReadFile(raw)
					if err != nil {
						t.Fatal(err)
					}
					var events []string
					var id string
					for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
						var record sessionIdentityRecord
						if err := json.Unmarshal([]byte(line), &record); err != nil {
							t.Fatal(err)
						}
						requireSessionUUIDv7(t, record.ID)
						if id == "" {
							id = record.ID
						}
						if record.ID != id || record.Header.ID != id || record.Persisted == noSession || (record.File == "") != noSession {
							t.Fatalf("inconsistent identity: %+v, want %s", record, id)
						}
						events = append(events, record.Event)
						if record.Event == "agent_end" && !noSession {
							persisted, err := os.ReadFile(record.File)
							if err != nil || !strings.Contains(string(persisted), `"id":"`+id+`"`) {
								t.Fatalf("persisted header: %s, %v", persisted, err)
							}
						}
					}
					want := []string{"session_start", "tool", "agent_end", "session_shutdown"}
					if len(events) < len(want) || !slices.Equal(events[:len(want)], want) || id == previous {
						t.Fatalf("events %v, want %v; identity %q, previous %q", events, want, id, previous)
					}
					previous = id
					files, err := os.ReadDir(dir)
					if err != nil || noSession && len(files) != 0 {
						t.Fatalf("--no-session wrote files: %v, %v", files, err)
					}
				}
			})
		}
	}
}
