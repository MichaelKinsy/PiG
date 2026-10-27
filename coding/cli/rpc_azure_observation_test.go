package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

var azureRPCRuns = flag.Int("rpc33-azure-runs", 200, "runs per Azure OpenAI Responses RPC observation case")

// The Agent, Session and RPC subscriber hops between the provider's Event Stream and the RPC writer determine which
// message_update records observe which provider state. Those hops belong to the executor-turn work for the Agent and Session
// (gap-d82 W3 and W7), so the complete-record comparison is opt-in until that lands.
var azureRPCFull = flag.Bool("rpc33-azure-full", false, "compare every message_update and message_end record, not only the assistant message_start")

const azureRPCOracle = "../../coding/testdata/rpc33-observation/providers/azure-openai-responses/rpc.json"

type azureRPCOracleFile struct {
	PiVersion string            `json:"piVersion"`
	Bodies    map[string]string `json:"bodies"`
	Cases     []struct {
		Shape   string            `json:"shape"`
		Fixture string            `json:"fixture"`
		Records []json.RawMessage `json:"records"`
	} `json:"cases"`
}

// TestRPCAzureOpenAIResponsesObservation runs the real pig binary in --mode rpc against one loopback Azure OpenAI Responses server and compares the
// first assistant message_start (the start state) with Pi 0.87.1's, run after run. With -rpc33-azure-full it compares every record of that message
// (message_start, each message_update, message_end).
// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363; packages/ai/src/api/azure-openai-responses.ts;
// oracle: coding/testdata/rpc33-observation/providers/azure-openai-responses/rpc.mjs.
func TestRPCAzureOpenAIResponsesObservation(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pig binary")
	}
	t.Parallel()
	data, err := os.ReadFile(azureRPCOracle)
	if err != nil {
		t.Fatal(err)
	}
	var oracle azureRPCOracleFile
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Cases) != 4 {
		t.Fatalf("oracle has %d cases; buffered and pending for tool and text require 4", len(oracle.Cases))
	}
	bin := buildPigBinaryForSignalTest(t)
	for _, c := range oracle.Cases {
		t.Run(c.Shape+"/"+c.Fixture, func(t *testing.T) {
			records := c.Records
			if !*azureRPCFull {
				records = records[:1]
			}
			want := canonicalRPCRecords(t, records)
			var failures atomic.Int32
			var wg sync.WaitGroup
			jobs := make(chan int)
			for range 8 {
				wg.Go(func() {
					for run := range jobs {
						got := runAzureRPCOnce(t, bin, oracle.Bodies, c.Shape, c.Fixture, len(records))
						if !bytes.Equal(got, want) && failures.Add(1) == 1 {
							diff := gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath("rpc"), string(want), string(got)))
							t.Errorf("run %d differs from Pi\n%v", run, diff)
						}
					}
				})
			}
			for run := range *azureRPCRuns {
				jobs <- run
			}
			close(jobs)
			wg.Wait()
			if n := failures.Load(); n != 0 {
				t.Errorf("%d of %d runs differ from Pi", n, *azureRPCRuns)
			}
		})
	}
}

func canonicalRPCRecords(t *testing.T, records []json.RawMessage) []byte {
	t.Helper()
	out := make([]any, len(records))
	for i, record := range records {
		if err := json.Unmarshal(record, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.MarshalIndent(canonicalizeRPCClocks(out), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// canonicalizeRPCClocks mirrors rpc.mjs: a numeric message timestamp above 1e9 is a wall clock.
func canonicalizeRPCClocks(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = canonicalizeRPCClocks(v[i])
		}
	case map[string]any:
		for k, item := range v {
			if k == "durationMs" { // the measured response time (Pi 1.1.0, #10549) depends on the clock, as timestamp does
				delete(v, k)
				continue
			}
			if number, ok := item.(float64); ok && k == "timestamp" && number > 1e9 {
				v[k] = float64(0)
				continue
			}
			v[k] = canonicalizeRPCClocks(item)
		}
	}
	return value
}

// azureServerTrace records what the loopback server of one runAzureRPCOnce saw and did, with the time since the run began, so
// a stall's failure message says whether the request arrived, whether the handler wrote a response and how much, and whether
// the connection was still open. A stalled run without it cannot tell a response the server never wrote from one that left the
// server and never reached PiG.
type azureServerTrace struct {
	mu     sync.Mutex
	start  time.Time
	events []string
}

func newAzureServerTrace() *azureServerTrace { return &azureServerTrace{start: time.Now()} }

func (trace *azureServerTrace) add(format string, args ...any) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.events = append(trace.events, fmt.Sprintf("+%.3fs ", time.Since(trace.start).Seconds())+fmt.Sprintf(format, args...))
}

func (trace *azureServerTrace) String() string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return strings.Join(trace.events, "\n\t")
}

// azureSocketSnapshot is the kernel's view of every TCP connection to or from the server's port. ss -tni gives each socket's
// state, queues and bytes sent, acknowledged and received, so the client's received bytes can be compared with the server's sent
// ones. Where ss is missing it reads /proc/net/tcp, which has the state and the queues but no byte counts.
func azureSocketSnapshot(port int) string {
	filter := "( sport = :" + strconv.Itoa(port) + " or dport = :" + strconv.Itoa(port) + " )"
	if out, err := exec.Command("ss", "-tni", "state", "all", filter).CombinedOutput(); err == nil {
		return strings.TrimSpace(string(out))
	}
	states := map[string]string{"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1", "05": "FIN_WAIT2", "06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT", "09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING"}
	suffix := fmt.Sprintf(":%04X", port)
	var lines []string
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			// sl local_address rem_address st tx_queue:rx_queue ...
			fields := strings.Fields(line)
			if len(fields) < 5 || (!strings.HasSuffix(fields[1], suffix) && !strings.HasSuffix(fields[2], suffix)) {
				continue
			}
			queues := strings.Split(fields[4], ":")
			if len(queues) != 2 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s local %s remote %s %s tx_queue 0x%s rx_queue 0x%s", table, fields[1], fields[2], states[fields[3]], queues[0], queues[1]))
		}
	}
	if len(lines) == 0 {
		return "no socket table available"
	}
	return strings.Join(lines, "\n")
}

func runAzureRPCOnce(t *testing.T, bin string, bodies map[string]string, shape, fixture string, keep int) []byte {
	t.Helper()
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	var requests atomic.Int32
	trace := newAzureServerTrace()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.add("handler enter %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		defer trace.add("handler exit")
		copied, copyErr := io.Copy(io.Discard, r.Body)
		trace.add("request body %d bytes, err %v", copied, copyErr)
		// Only the model's Responses request takes a response slot. A foreign client that reaches this reused loopback port must not consume the first body, or PiG's first request would receive the follow-up body.
		if r.Method != http.MethodPost || r.URL.Path != "/openai/v1/responses" {
			trace.add("not the Responses request: 404")
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if number := requests.Add(1); number != 1 {
			trace.add("request %d: follow-up body", number)
			n, err := io.WriteString(w, bodies["followup"])
			trace.add("wrote %d bytes, err %v", n, err)
			return
		}
		if fixture == "buffered" {
			n, err := io.WriteString(w, bodies[shape])
			trace.add("request 1: buffered body written %d bytes, err %v (sent when the handler returns)", n, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		trace.add("request 1: headers flushed, waiting for the release")
		select {
		case <-release:
			n, err := io.WriteString(w, bodies[shape])
			trace.add("released: body written %d bytes, err %v", n, err)
		case <-r.Context().Done():
			trace.add("request context done before the release: %v", r.Context().Err())
		}
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		trace.add("connection %s -> %s: %s", conn.RemoteAddr(), conn.LocalAddr(), state)
	}
	server.Start()
	defer server.Close()
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"providers":{"p":{"api":"azure-openai-responses","baseUrl":%q,"apiKey":"k","models":[{"id":"strict","name":"strict","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":1000}]}}}`, server.URL+"/openai/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "parity-read-target.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--mode", "rpc", "--offline", "--no-extensions", "--model", "p/strict", "--no-context-files", "--no-skills", "--session-dir", filepath.Join(dir, "s"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_HOME="+filepath.Join(dir, "pighome"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if _, err := io.WriteString(stdin, `{"id":"read","type":"prompt","message":"READ"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		records []json.RawMessage
		err     error
	}
	done := make(chan result, 1)
	go func() {
		var records []json.RawMessage
		open := false
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(nil, 16<<20)
		for scanner.Scan() {
			var e struct {
				Type    string `json:"type"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			line := append([]byte(nil), scanner.Bytes()...)
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			assistant := e.Message.Role == "assistant"
			if e.Type == "message_start" && assistant && !open {
				open = true
				releaseOnce()
			}
			if open && (e.Type == "message_update" || (assistant && (e.Type == "message_start" || e.Type == "message_end"))) {
				records = append(records, line)
			}
			if open && assistant && e.Type == "message_end" {
				done <- result{records: records}
				return
			}
		}
		done <- result{err: fmt.Errorf("stdout closed before assistant message_end: %w", scanner.Err())}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Errorf("%v; stderr: %s", r.err, stderr.String())
			return nil
		}
		return canonicalRPCRecords(t, r.records[:min(keep, len(r.records))])
	case <-time.After(testbudget.Wait(t)):
		// A stalled run names the side that holds the response. The server's trace says whether the handler wrote one; the
		// kernel's socket table, read while the child is still alive, says whether those bytes were sent and received (the
		// server's bytes_sent against the client's bytes_received) and what is queued unread on either end; and SIGQUIT makes
		// the child's Go runtime dump every goroutine to stderr, which says what PiG was waiting on.
		sockets, events := azureSocketSnapshot(server.Listener.Addr().(*net.TCPAddr).Port), trace.String()
		dump := dumpStalledChild(cmd, stderr)
		t.Errorf("timeout after %d Responses request(s); server %s\nserver trace:\n\t%s\nsockets while the child waited:\n%s\nchild stderr: %s", requests.Load(), server.URL, events, sockets, dump)
		return nil
	}
}

// dumpStalledChild asks a hung child for its goroutine dump and returns its stderr once it has exited. Where the platform
// has no SIGQUIT it returns what the child had written.
func dumpStalledChild(cmd *exec.Cmd, stderr *lockedBuffer) string {
	if runtime.GOOS == "windows" || cmd.Process.Signal(syscall.SIGQUIT) != nil {
		return stderr.String()
	}
	// The deferred Kill/Wait in the caller owns the exit; the dump is complete when the runtime has written it, so poll the
	// buffer's growth rather than racing Wait.
	for previous := -1; previous != len(stderr.String()); {
		previous = len(stderr.String())
		time.Sleep(200 * time.Millisecond)
	}
	return stderr.String()
}
