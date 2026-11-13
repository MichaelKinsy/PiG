package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// rpcOneChunkStep writes its records in one write, if it has any, and reads records until one has the tag until.
type rpcOneChunkStep struct {
	write []string
	until string
}

// rpcOneChunkTag names the records that tell how a prompt was answered and whether it ran: a response by its id and its disposition, error
// or isStreaming, and the run events.
func rpcOneChunkTag(r rpcRecord) string {
	switch r["type"] {
	case "response":
		data, _ := r["data"].(map[string]any)
		switch {
		case r["success"] != true:
			return fmt.Sprintf("%v:error:%v", r["id"], r["error"])
		case r["command"] == "get_state":
			return fmt.Sprintf("%v:streaming=%v", r["id"], data["isStreaming"])
		}
		return fmt.Sprintf("%v:%v", r["id"], data["disposition"])
	case "agent_start", "agent_end", "agent_settled":
		return fmt.Sprint(r["type"])
	}
	return ""
}

// startPiRPCFaux starts the pinned Pi in RPC mode with Pi's test-faux provider, env and the further arguments args.
func startPiRPCFaux(t *testing.T, env []string, args ...string) *rpcProcess {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	piRoot := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("Pi version = %q, %v", pkg.Version, err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	argv := append([]string{filepath.Join(piRoot, "dist", "cli.js"), "--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1",
		"-e", filepath.Join(root, "test", "parity", "testdata", "test-faux-provider.ts")}, args...)
	return startJSONLProcessAt(t, t.TempDir(), append([]string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent")}, env...), node, argv...)
}

func startPigRPCOneChunk(t *testing.T) *rpcProcess {
	t.Helper()
	home := t.TempDir()
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}
	return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1")
}

// D629, both sides. Two prompts written to standard input in one write: the pinned Pi answers both "started", emits an agent_settled for
// the second at once and runs only the first; the second gets no error and no run, and from that agent_settled on Pi reports isStreaming
// false while the first run streams, so a third prompt is answered "started" and dropped the same way (agent-session.ts:2191 and :2227,
// :1944, :1960, :1969, :1986 and :1141; agent.ts:383-387; rpc-mode.ts:425-429). PiG rejects the second, and the third, with the "already
// processing" error and reports isStreaming true. Written one after the other, the second prompt is rejected by both. The Pi rows fail
// the day Pi changes its side: that is when D629's remove-when is checked.
func TestRPCPromptsWrittenInOneChunkDivergeFromPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and runs the pinned Pi under Node")
	}
	prompt := func(id, message string) string {
		return fmt.Sprintf(`{"id":%q,"type":"prompt","message":%q}`, id, message)
	}
	state := func(id string) []string { return []string{fmt.Sprintf(`{"id":%q,"type":"get_state"}`, id)} }
	// A prompt of test-faux whose reply streams for about two seconds.
	const long = "TUI_LIVE_STREAM_PAUSE"
	rejected := func(id string) string { return id + ":error:" + errAgentAlreadyProcessingMessage }
	settle := []rpcOneChunkStep{{nil, "agent_end"}, {nil, "agent_settled"}, {state("d"), "d:streaming=false"}}
	type side struct {
		steps []rpcOneChunkStep
		want  []string
	}
	// Written after the first prompt's agent_start, the second is rejected by both.
	sequential := side{
		steps: append([]rpcOneChunkStep{{[]string{prompt("p", long)}, "agent_start"}, {[]string{prompt("p2", "hi2")}, rejected("p2")}}, settle...),
		want:  []string{"p:started", "agent_start", rejected("p2"), "agent_end", "agent_settled", "d:streaming=false"},
	}
	cases := []struct {
		name    string
		message string
		pi, pig side
	}{
		{
			name: "one chunk", message: "hi",
			pi: side{
				steps: append([]rpcOneChunkStep{{[]string{prompt("p", "hi"), prompt("p2", "hi2")}, "agent_settled"}}, settle...),
				want:  []string{"p:started", "p2:started", "agent_start", "agent_settled", "agent_end", "agent_settled", "d:streaming=false"},
			},
			pig: side{
				steps: append([]rpcOneChunkStep{{[]string{prompt("p", "hi"), prompt("p2", "hi2")}, "agent_start"}}, settle...),
				want:  []string{rejected("p2"), "p:started", "agent_start", "agent_end", "agent_settled", "d:streaming=false"},
			},
		},
		{
			name: "one chunk, then a state and a prompt while the first runs", message: long,
			pi: side{
				steps: append([]rpcOneChunkStep{
					{[]string{prompt("p", long), prompt("p2", "hi2")}, "agent_settled"},
					{state("h"), "h:streaming=false"},
					{[]string{prompt("p3", "hi3")}, "agent_settled"},
					{state("h2"), "h2:streaming=false"},
				}, settle...),
				want: []string{"p:started", "p2:started", "agent_start", "agent_settled", "h:streaming=false", "p3:started", "agent_settled", "h2:streaming=false", "agent_end", "agent_settled", "d:streaming=false"},
			},
			pig: side{
				steps: append([]rpcOneChunkStep{
					{[]string{prompt("p", long), prompt("p2", "hi2")}, "agent_start"},
					{state("h"), "h:streaming=true"},
					{[]string{prompt("p3", "hi3")}, rejected("p3")},
					{state("h2"), "h2:streaming=true"},
				}, settle...),
				want: []string{rejected("p2"), "p:started", "agent_start", "h:streaming=true", rejected("p3"), "h2:streaming=true", "agent_end", "agent_settled", "d:streaming=false"},
			},
		},
		{name: "one after the other", message: long, pi: sequential, pig: sequential},
	}
	for _, tc := range cases {
		for _, impl := range []struct {
			name  string
			start func(*testing.T) *rpcProcess
			side  side
		}{{"pi", func(t *testing.T) *rpcProcess { return startPiRPCFaux(t, nil) }, tc.pi}, {"pig", startPigRPCOneChunk, tc.pig}} {
			t.Run(tc.name+"/"+impl.name, func(t *testing.T) {
				p := impl.start(t)
				var seen, users []string
				var messages any
				for _, step := range impl.side.steps {
					if len(step.write) > 0 {
						chunk := ""
						for _, record := range step.write {
							chunk += record + "\n"
						}
						if _, err := p.stdin.Write([]byte(chunk)); err != nil {
							t.Fatal(err)
						}
					}
					p.awaitProgress(func() string { return fmt.Sprintf("%q after %q", step.until, seen) }, func(record rpcRecord) bool {
						if record["type"] == "message_start" {
							if message, _ := record["message"].(map[string]any); message["role"] == "user" {
								content, _ := json.Marshal(message["content"])
								users = append(users, string(content))
							}
						}
						if record["id"] == "d" {
							data, _ := record["data"].(map[string]any)
							messages = data["messageCount"]
						}
						tag := rpcOneChunkTag(record)
						if tag == "" {
							return false
						}
						seen = append(seen, tag)
						return tag == step.until
					})
				}
				if !slices.Equal(seen, impl.side.want) {
					t.Errorf("records =\n  %q, want\n  %q", seen, impl.side.want)
				}
				// Only the first prompt's message exists and only its run was made: the system message, the prompt and the reply.
				if want := []string{fmt.Sprintf(`[{"text":%q,"type":"text"}]`, tc.message)}; !slices.Equal(users, want) {
					t.Errorf("user messages = %q, want %q", users, want)
				}
				if messages != float64(3) {
					t.Errorf("messageCount = %v, want 3", messages)
				}
				p.closeAndWait("after the run")
			})
		}
	}
}
