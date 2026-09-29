package inproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func upstreamUserBashRunner(t *testing.T, handler extension.HandlerFn) (*inproc.Runner, extension.UserBashEvent, *[]*extension.ExtensionError) {
	t.Helper()
	cwd := t.TempDir()
	r := inproc.NewRunner([]extension.Extension{{Path: "handler.ts", Handlers: map[string][]extension.HandlerFn{"user_bash": {handler}}}}, cwd)
	reported := new([]*extension.ExtensionError)
	r.AddErrorListener(func(err *extension.ExtensionError) { *reported = append(*reported, err) })
	return r, extension.UserBashEvent{Type: "user_bash", Command: "pwd", ExcludeFromContext: false, Cwd: cwd}, reported
}

func TestUpstreamRunnerUserBash(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:629
	t.Run("fails closed when a user_bash handler throws", func(t *testing.T) {
		r, event, reported := upstreamUserBashRunner(t, func(...any) (any, error) { return nil, errors.New("Routing failed") })
		_, err := r.EmitUserBash(t.Context(), event)
		if err == nil || !strings.Contains(err.Error(), "Routing failed") {
			t.Fatalf("error=%v", err)
		}
		if len(*reported) != 1 || (*reported)[0].Event != "user_bash" || (*reported)[0].Error != "Routing failed" {
			t.Fatalf("reported=%+v", *reported)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:651 (all six table rows).
	for _, tc := range []struct {
		name   string
		result any
	}{
		{"an empty object", json.RawMessage(`{}`)},
		{"null operations", json.RawMessage(`{"operations":null}`)},
		{"operations without exec", json.RawMessage(`{"operations":{}}`)},
		{"a null result", json.RawMessage(`{"result":null}`)},
		{"an incomplete result", json.RawMessage(`{"result":{"output":"handled"}}`)},
		{"operations and a result", &extension.UserBashEventResult{Operations: upstreamBashOperations{}, Result: map[string]any{"output": "handled", "exitCode": 0, "cancelled": false, "truncated": false}}},
	} {
		t.Run("fails closed when a user_bash handler returns "+tc.name, func(t *testing.T) {
			r, event, reported := upstreamUserBashRunner(t, func(...any) (any, error) { return tc.result, nil })
			_, err := r.EmitUserBash(t.Context(), event)
			const want = "Invalid user_bash handler result"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v", err)
			}
			if len(*reported) != 1 || (*reported)[0].Event != "user_bash" || !strings.Contains((*reported)[0].Error, want) {
				t.Fatalf("reported=%+v", *reported)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:682
	t.Run("accepts valid user_bash operations and result overrides", func(t *testing.T) {
		wantResult := map[string]any{"output": "handled", "exitCode": 0, "cancelled": false, "truncated": false}
		r, event, _ := upstreamUserBashRunner(t, func(args ...any) (any, error) {
			if args[0].(extension.UserBashEvent).Command == "operations" {
				return &extension.UserBashEventResult{Operations: upstreamBashOperations{}}, nil
			}
			return &extension.UserBashEventResult{Result: wantResult}, nil
		})
		event.Command = "operations"
		operations, err := r.EmitUserBash(t.Context(), event)
		if err != nil || operations == nil || operations.Operations == nil || operations.Result != nil {
			t.Fatalf("operations=%+v error=%v", operations, err)
		}
		event.Command = "result"
		result, err := r.EmitUserBash(t.Context(), event)
		if err != nil || !reflect.DeepEqual(result, &extension.UserBashEventResult{Result: wantResult}) {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	})
}

type upstreamBashOperations struct{}

func (upstreamBashOperations) Exec(context.Context, string, string, extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	return extension.BashOperationsResult{ExitCode: new(0)}, nil
}

// A required number-or-undefined property uses a present nil value in native Go.
func TestUserBashPresentUndefinedExitCode(t *testing.T) {
	for _, value := range []any{
		&extension.UserBashEventResult{Result: map[string]any{"output": "handled", "exitCode": nil, "cancelled": false, "truncated": false}},
		json.RawMessage(`{"result":{"output":"handled","cancelled":false,"truncated":false},"_pigUserBashExitCodeUndefined":true}`),
	} {
		r, event, _ := upstreamUserBashRunner(t, func(...any) (any, error) { return value, nil })
		result, err := r.EmitUserBash(t.Context(), event)
		if err != nil || result == nil {
			t.Fatalf("result=%+v error=%v", result, err)
		}
		exitCode, present := result.Result.(map[string]any)["exitCode"]
		if !present || exitCode != nil {
			t.Fatalf("exitCode=%v present=%v", exitCode, present)
		}
	}
}

// Pi runner.ts:152-158 requires exitCode to exist and accepts undefined, not null.
func TestUserBashRejectsMissingOrNullResultFields(t *testing.T) {
	for _, raw := range []string{
		`{"result":{"output":"handled","cancelled":false,"truncated":false}}`,
		`{"result":{"output":"handled","exitCode":null,"cancelled":false,"truncated":false}}`,
		`{"result":{"output":"handled","exitCode":0,"cancelled":false,"truncated":false,"fullOutputPath":null}}`,
		`{"operations":null,"result":{"output":"handled","exitCode":0,"cancelled":false,"truncated":false}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			r, event, reported := upstreamUserBashRunner(t, func(...any) (any, error) { return json.RawMessage(raw), nil })
			got, err := r.EmitUserBash(t.Context(), event)
			if err == nil || got != nil || len(*reported) != 1 {
				t.Fatalf("result=%+v error=%v reported=%+v", got, err, *reported)
			}
		})
	}
}
