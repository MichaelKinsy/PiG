package subprocess

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// userBashOperationsExec runs one Exec through a user_bash operations handle against a scripted extension: settle answers the exec request
// with the raw JSON result, after the extension has streamed updates as onData chunks.
func userBashOperationsExec(t *testing.T, options extension.BashOperationsExecOptions, updates []string, result string) (UserBashExecArgs, extension.BashOperationsResult, error) {
	t.Helper()
	conn, peer := userBashOperationsRig(t)
	typed, ok := userBashOperationsResult(conn, json.RawMessage(`{"operations":{"handle":"bash-9"}}`))
	if !ok {
		t.Fatal("no operations result")
	}
	type outcome struct {
		result extension.BashOperationsResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		got, err := typed.Operations.Exec(t.Context(), "make test", "/repo", options)
		done <- outcome{got, err}
	}()
	request, err := tryReadFramed(peer)
	if err != nil || request.Request == nil || request.Request.Method != RequestUserBashExec || request.Request.Tool != "bash-9" {
		t.Fatalf("request = %+v, %v, want user_bash_exec for bash-9", request, err)
	}
	var args UserBashExecArgs
	if err := json.Unmarshal(request.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	for _, update := range updates {
		payload, err := json.Marshal(ToolUpdatePayload{RequestID: request.ID, Result: json.RawMessage(update)})
		if err != nil {
			t.Fatal(err)
		}
		writeFramed(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyToolUpdate, Args: payload}})
	}
	response := &ResponsePayload{}
	if result != "" {
		response.Result = json.RawMessage(result)
	}
	writeFramed(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: response})
	settled := <-done
	return args, settled.result, settled.err
}

// Pi's BashOperations.exec resolves { exitCode: number | null }: the number is the command's exit status, null (or no status) is a failed command, and
// bash.ts hands the status through unchanged.
func TestUserBashOperationsExecReturnsTheExitCodeTheExtensionReported(t *testing.T) {
	for name, tc := range map[string]struct {
		result string
		want   *int
	}{
		"zero":         {`{"exitCode":0}`, new(0)},
		"failure code": {`{"exitCode":3}`, new(3)},
		"signal":       {`{"exitCode":137}`, new(137)},
		"null":         {`{"exitCode":null}`, nil},
		"no result":    {``, nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, got, err := userBashOperationsExec(t, extension.BashOperationsExecOptions{}, nil, tc.result)
			if err != nil {
				t.Fatalf("Exec error = %v", err)
			}
			if !reflect.DeepEqual(got.ExitCode, tc.want) {
				t.Fatalf("ExitCode = %v, want %v", deref(got.ExitCode), deref(tc.want))
			}
		})
	}
}

func deref(code *int) any {
	if code == nil {
		return nil
	}
	return *code
}

// The extension's exec receives the command, the working directory, the timeout in seconds and the environment as a map: each KEY=VALUE entry
// splits at its first "=", and an entry without one is not a variable. A nil environment is absent, not empty.
func TestUserBashOperationsExecSendsTheCommandTimeoutAndEnvironmentToTheExtension(t *testing.T) {
	args, _, err := userBashOperationsExec(t, extension.BashOperationsExecOptions{Timeout: new(2.5), Env: []string{"A=1", "B=x=y", "EMPTY=", "no-equals"}}, nil, `{"exitCode":0}`)
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := map[string]string{"A": "1", "B": "x=y", "EMPTY": ""}
	if args.Command != "make test" || args.Cwd != "/repo" || args.Timeout == nil || *args.Timeout != 2.5 || args.Env == nil || !reflect.DeepEqual(*args.Env, wantEnv) {
		t.Fatalf("args = %+v (env %v), want make test in /repo with timeout 2.5 and env %v", args, args.Env, wantEnv)
	}
	args, _, err = userBashOperationsExec(t, extension.BashOperationsExecOptions{Env: []string{}}, nil, `{"exitCode":0}`)
	if err != nil || args.Env == nil || len(*args.Env) != 0 {
		t.Fatalf("an empty, non-nil environment = %+v, %v, want an empty object", args.Env, err)
	}
}

// onData receives each chunk the extension streams, decoded from base64 and in order, before Exec settles. A chunk that is not base64 does not
// stop the exec: the chunks around it are delivered and Exec returns the decode error once the extension settles.
func TestUserBashOperationsExecDeliversStreamedOutputInOrderAndReportsABadChunk(t *testing.T) {
	var chunks []string
	collect := func(data []byte) { chunks = append(chunks, string(data)) }
	encode := func(text string) string {
		return `{"data":"` + base64.StdEncoding.EncodeToString([]byte(text)) + `"}`
	}
	_, got, err := userBashOperationsExec(t, extension.BashOperationsExecOptions{OnData: collect}, []string{encode("one\n"), encode("t\xe2\x82"), encode("wo")}, `{"exitCode":0}`)
	if err != nil || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("Exec = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(chunks, []string{"one\n", "t\xe2\x82", "wo"}) {
		t.Fatalf("chunks = %q, want the three chunks as sent, raw bytes intact", chunks)
	}

	chunks = nil
	_, got, err = userBashOperationsExec(t, extension.BashOperationsExecOptions{OnData: collect}, []string{encode("before"), `{"data":"***"}`, encode("after")}, `{"exitCode":0}`)
	if err == nil || !strings.Contains(err.Error(), "decode user_bash output") || got.ExitCode != nil {
		t.Fatalf("Exec = %+v, %v, want the decode error and no result", got, err)
	}
	if !reflect.DeepEqual(chunks, []string{"before", "after"}) {
		t.Fatalf("chunks = %q, want the chunks around the bad one", chunks)
	}

	// Output with no consumer is dropped without failing the exec.
	if _, _, err := userBashOperationsExec(t, extension.BashOperationsExecOptions{}, []string{encode("ignored")}, `{"exitCode":0}`); err != nil {
		t.Fatalf("Exec without OnData = %v", err)
	}
}

// executeBashWithOperations matches the abort by its message: a connection lost while the exec's signal is aborted is "aborted", and one lost
// without an abort keeps its own error.
func TestUserBashOperationsExecReportsAbortedOnlyWhenTheContextWasCancelled(t *testing.T) {
	run := func(cancelFirst bool) error {
		conn, peer := userBashOperationsRig(t)
		typed, ok := userBashOperationsResult(conn, json.RawMessage(`{"operations":{"handle":"bash-8"}}`))
		if !ok {
			t.Fatal("no operations result")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		failed := make(chan error, 1)
		go func() {
			_, err := typed.Operations.Exec(ctx, "sleep", "/work", extension.BashOperationsExecOptions{})
			failed <- err
		}()
		if _, err := tryReadFramed(peer); err != nil {
			t.Fatal(err)
		}
		if cancelFirst {
			cancel()
			if frame, err := tryReadFramed(peer); err != nil || frame.Type != MsgCancel {
				t.Fatalf("frame after the cancellation = %+v, %v, want a cancel", frame, err)
			}
		}
		_ = conn.Close("the extension went away")
		return <-failed
	}
	if err := run(true); err == nil || err.Error() != "aborted" {
		t.Fatalf("Exec after a cancellation and a lost connection = %v, want aborted", err)
	}
	if err := run(false); err == nil || err.Error() == "aborted" {
		t.Fatalf("Exec after a lost connection without a cancellation = %v, want the connection's own error", err)
	}
}
