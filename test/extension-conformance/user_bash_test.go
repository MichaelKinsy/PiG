package extensionconformance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func conformanceUserBashResult(command string) any {
	result := map[string]any{"output": "handled", "exitCode": 7, "cancelled": false, "truncated": false}
	switch command {
	case "valid":
	case "undefined":
		result["exitCode"] = nil
	case "undefined-path":
		result["fullOutputPath"] = nil
	case "missing":
		delete(result, "exitCode")
	case "invalid":
		result["exitCode"] = "invalid"
	case "null-operations":
		return json.RawMessage(`{"operations":null,"result":{"output":"handled","exitCode":7,"cancelled":false,"truncated":false}}`)
	default:
		return nil
	}
	return &extension.UserBashEventResult{Result: result}
}

// Pi runner.ts:136-159 distinguishes a missing property from number | undefined. Nil/None/null in the native SDK's required optional exit-code field is undefined; explicit wire null remains invalid.
func TestUserBashValidationAcrossSDKs(t *testing.T) {
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var reported []*extension.ExtensionError
			h.runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
			for _, command := range []string{"valid", "undefined", "undefined-path", "missing", "invalid", "null-operations", "observe"} {
				t.Run(command, func(t *testing.T) {
					reported = nil
					result, err := h.runner.EmitUserBash(t.Context(), extension.UserBashEvent{Type: "user_bash", Command: command, Cwd: t.TempDir()})
					switch command {
					case "observe":
						if err != nil || result != nil || len(reported) != 0 {
							t.Fatalf("result=%+v error=%v reported=%v", result, err, reported)
						}
					case "missing", "invalid", "null-operations":
						if err == nil || !strings.Contains(err.Error(), "Invalid user_bash handler result") || result != nil || len(reported) != 1 {
							t.Fatalf("result=%+v error=%v reported=%v", result, err, reported)
						}
					default:
						if err != nil || result == nil || len(reported) != 0 {
							t.Fatalf("result=%+v error=%v reported=%v", result, err, reported)
						}
						record, ok := result.Result.(map[string]any)
						if !ok || record["output"] != "handled" || record["cancelled"] != false || record["truncated"] != false {
							t.Fatalf("result=%+v", result)
						}
						exit, present := record["exitCode"]
						encoded, err := json.Marshal(exit)
						want := "7"
						if command == "undefined" {
							want = "null"
						}
						if err != nil || !present || string(encoded) != want {
							t.Fatalf("exitCode=%v present=%v encoding=%s error=%v", exit, present, encoded, err)
						}
						if record["fullOutputPath"] != nil {
							t.Fatalf("fullOutputPath=%v", record["fullOutputPath"])
						}
					}
				})
			}
		})
	}
}
