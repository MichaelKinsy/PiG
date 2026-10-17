package cli

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi rpc-mode.ts:64-77,385-387 copies command.id and command.type into each response, and serializeJsonLine emits JSON.stringify(JSON.parse(line)) values: any JSON id is echoed in its JavaScript form, member names match exactly, and an absent member stays absent. A malformed line answers with error(undefined, "parse", ...), and a non-object command has neither member (rpc-mode.ts:716-717). AgentSession.executeBash forwards the same id to bash_execution_update (agent-session.ts:3479). Expected lines are pinned Pi 0.87.1 output for the same input.
func TestRPCRequestIDsEchoJSONValues(t *testing.T) {
	const abort = `"type":"response","command":"abort_retry","success":true}`
	unknown := func(id, command, message string) string {
		return `{` + id + `"type":"response",` + command + `"success":false,"error":"Unknown command: ` + message + `"}`
	}
	process := startRawRPCProcess(t)
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"null", `{"id":null,"type":"abort_retry"}`, []string{`{"id":null,` + abort}},
		{"integer", `{"id":7,"type":"abort_retry"}`, []string{`{"id":7,` + abort}},
		{"integral fraction", `{"id":1.0,"type":"abort_retry"}`, []string{`{"id":1,` + abort}},
		{"exponent", `{"id":1e2,"type":"abort_retry"}`, []string{`{"id":100,` + abort}},
		{"negative zero", `{"id":-0,"type":"abort_retry"}`, []string{`{"id":0,` + abort}},
		{"overflow", `{"id":1e400,"type":"abort_retry"}`, []string{`{"id":null,` + abort}},
		{"unsafe integer", `{"id":12345678901234567890,"type":"abort_retry"}`, []string{`{"id":12345678901234567000,` + abort}},
		{"small plain", `{"id":0.000001,"type":"abort_retry"}`, []string{`{"id":0.000001,` + abort}},
		{"small exponent", `{"id":1e-7,"type":"abort_retry"}`, []string{`{"id":1e-7,` + abort}},
		{"large exponent", `{"id":1e21,"type":"abort_retry"}`, []string{`{"id":1e+21,` + abort}},
		{"string escapes", `{"id":"A\ud800<>& \u007f\u0001\/","type":"abort_retry"}`, []string{"{\"id\":\"A\\ud800<>& \x7f\\u0001/\"," + abort}},
		{"object order", `{"id":{"b":1,"a":2,"10":3,"2":4,"b":5,"-1":6,"01":7,"4294967295":8,"4294967294":9},"type":"abort_retry"}`, []string{`{"id":{"2":4,"10":3,"4294967294":9,"b":5,"a":2,"-1":6,"01":7,"4294967295":8},` + abort}},
		{"array", `{"id":[1, 2 ,"x", {"z":null}],"type":"abort_retry"}`, []string{`{"id":[1,2,"x",{"z":null}],` + abort}},
		{"proto member", `{"id":{"__proto__":1},"type":"abort_retry"}`, []string{`{"id":{"__proto__":1},` + abort}},
		{"true", `{"id":true,"type":"abort_retry"}`, []string{`{"id":true,` + abort}},
		{"false", `{"id":false,"type":"abort_retry"}`, []string{`{"id":false,` + abort}},
		{"case-sensitive id", `{"ID":"upper","type":"abort_retry"}`, []string{`{` + abort}},
		{"duplicate id", `{"id":"a","id":"b","type":"abort_retry"}`, []string{`{"id":"b",` + abort}},
		{"case-sensitive type", `{"Type":"abort_retry","id":"case"}`, []string{unknown(`"id":"case",`, ``, `undefined`)}},
		{"number type", `{"id":"t5","type":5}`, []string{unknown(`"id":"t5",`, `"command":5,`, `5`)}},
		{"null type", `{"id":"tnull","type":null}`, []string{unknown(`"id":"tnull",`, `"command":null,`, `null`)}},
		{"infinite type", `{"id":"inf","type":1e400}`, []string{unknown(`"id":"inf",`, `"command":null,`, `Infinity`)}},
		{"exponent type", `{"id":"num","type":1e21}`, []string{unknown(`"id":"num",`, `"command":1e+21,`, `1e+21`)}},
		{"lone surrogate type", `{"id":"lone","type":"\ud800x"}`, []string{unknown(`"id":"lone",`, `"command":"\ud800x",`, `\ud800x`)}},
		{"boolean type", `{"id":"bool","type":true}`, []string{unknown(`"id":"bool",`, `"command":true,`, `true`)}},
		{"empty type", `{"id":"empty","type":""}`, []string{unknown(`"id":"empty",`, `"command":"",`, ``)}},
		{"object type", `{"id":"obj","type":{"a":1}}`, []string{unknown(`"id":"obj",`, `"command":{"a":1},`, `[object Object]`)}},
		{"array type", `{"id":"arr","type":[1,[2,null],"x"]}`, []string{unknown(`"id":"arr",`, `"command":[1,[2,null],"x"],`, `1,2,,x`)}},
		{"mixed array type", `{"id":"arrobj","type":[{"a":1},null,[]]}`, []string{unknown(`"id":"arrobj",`, `"command":[{"a":1},null,[]],`, `[object Object],,`)}},
		{"shadowed toString", `{"id":"objerr","type":{"toString":null}}`, []string{`{"id":"objerr","type":"response","command":{"toString":null},"success":false,"error":"Cannot convert object to primitive value"}`}},
		{"nested shadowed toString", `{"id":"arrerr","type":[{"toString":3}]}`, []string{`{"id":"arrerr","type":"response","command":[{"toString":3}],"success":false,"error":"Cannot convert object to primitive value"}`}},
		{"shadowed valueOf", `{"id":"valueof","type":{"valueOf":null}}`, []string{unknown(`"id":"valueof",`, `"command":{"valueOf":null},`, `[object Object]`)}},
		{"number command", `5`, []string{unknown(``, ``, `undefined`)}},
		{"string command", `"str"`, []string{unknown(``, ``, `undefined`)}},
		{"array command", `[]`, []string{unknown(``, ``, `undefined`)}},
		{"malformed", `{"id":"x",`, []string{`{"type":"response","command":"parse","success":false,"error":"Failed to parse command: Expected double-quoted property name in JSON at position 10 (line 1 column 11)"}`}},
		{"bash number", `{"id":7,"type":"bash","command":"printf hi"}`, []string{
			`{"type":"bash_execution_update","id":7,"delta":"hi"}`,
			`{"id":7,"type":"response","command":"bash","success":true,"data":{"output":"hi","exitCode":0,"cancelled":false,"truncated":false}}`,
		}},
		{"bash null", `{"id":null,"type":"bash","command":"printf yo"}`, []string{
			`{"type":"bash_execution_update","id":null,"delta":"yo"}`,
			`{"id":null,"type":"response","command":"bash","success":true,"data":{"output":"yo","exitCode":0,"cancelled":false,"truncated":false}}`,
		}},
		{"bash object", `{"id":{"k":[1.50]},"type":"bash","command":"printf ok"}`, []string{
			`{"type":"bash_execution_update","id":{"k":[1.5]},"delta":"ok"}`,
			`{"id":{"k":[1.5]},"type":"response","command":"bash","success":true,"data":{"output":"ok","exitCode":0,"cancelled":false,"truncated":false}}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process.t = t
			process.send(tc.input)
			// Commands run concurrently, as in Pi, so the sync command follows the awaited response rather than the input line.
			got := []string{process.line()}
			for !strings.Contains(got[len(got)-1], `"type":"response"`) {
				got = append(got, process.line())
			}
			process.send(`{"id":"sync","type":"abort_retry"}`)
			for line := process.line(); line != `{"id":"sync",`+abort; line = process.line() {
				got = append(got, line)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("input %s\n got %q\nwant %q", tc.input, got, tc.want)
			}
		})
	}
	process.t = t
	process.close()
}

// The rv-rpc release task explicitly retains PiG's null-command response pending divergence review. Pi 0.87.1 rpc-mode.ts:387,792-796 dereferences command.id again in the error handler and exits 1 instead.
func TestRPCNullCommandRetainsUnknownResponse(t *testing.T) {
	process := startRawRPCProcess(t)
	process.send("null")
	want := `{"type":"response","success":false,"error":"Unknown command: undefined"}`
	if got := process.line(); got != want {
		t.Fatalf("null command response=%q, want=%q", got, want)
	}
	process.close()
}

// rpcStringID is a string request id in its wire form.
func rpcStringID(value string) rpcRequestID {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

// rawRPCProcess reads RPC stdout lines as bytes, so tests compare the serialized wire form rather than a decoded value.
type rawRPCProcess struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr bytes.Buffer
	lines  chan string
	budget time.Duration
}

func startRawRPCProcess(t *testing.T) *rawRPCProcess {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(buildPigBinaryForSignalTest(t), "--mode", "rpc", "--no-session", "--no-extensions")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_OFFLINE=1")
	p := &rawRPCProcess{t: t, cmd: cmd, lines: make(chan string, 16), budget: testbudget.Wait(t)}
	cmd.Stderr = &p.stderr
	var err error
	if p.stdin, err = cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(p.lines)
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			p.lines <- line[:len(line)-1]
		}
	}()
	t.Cleanup(func() {
		_ = p.stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return p
}

func (p *rawRPCProcess) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		p.t.Fatal(err)
	}
}

func (p *rawRPCProcess) line() string {
	p.t.Helper()
	select {
	case line, ok := <-p.lines:
		if !ok {
			p.t.Fatalf("RPC output ended\n%s", p.stderr.String())
		}
		return line
	case <-time.After(p.budget):
		p.t.Fatalf("RPC output timed out after %s\n%s", p.budget, p.stderr.String())
		return ""
	}
}

func (p *rawRPCProcess) close() {
	p.t.Helper()
	_ = p.stdin.Close()
	for line := range p.lines {
		p.t.Errorf("unexpected RPC output after the awaited records: %s", line)
	}
	if err := p.cmd.Wait(); err != nil {
		p.t.Errorf("RPC process: %v\n%s", err, p.stderr.String())
	}
}
