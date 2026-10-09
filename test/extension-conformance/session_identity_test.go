package extensionconformance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's getSessionId/getSessionFile/getLeafId read the current SessionManager (session-manager.ts:1152-1166). Context convenience methods must return the same identity as the SessionManager facade, including cleared file/leaf state after replacement.
func TestContextSessionIdentityAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := allHarnessCases()
	for _, language := range []string{"go", "rust", "python"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test complete")
				}
			})
			for _, state := range []struct{ id, file, leaf, name string }{
				{"persisted-session", "/sessions/first.jsonl", "first-leaf", "Named session ü"},
				{"memory-session", "", "", ""},
				{"replacement-session", "/sessions/replaced.jsonl", "replacement-leaf", "Renamed"},
			} {
				// The native SessionManager is the reference; a nonempty sentinel catches every SDK's empty fallback.
				reference := codingagent.NewSession(state.id, t.TempDir())
				reference.SetPath(state.file)
				// The name entry precedes the leaf entry so it does not become the leaf.
				if state.name != "" {
					reference.SetPath("")
					if _, err := reference.AppendSessionInfo(state.name); err != nil {
						t.Fatal(err)
					}
					reference.SetPath(state.file)
				}
				if state.leaf != "" {
					raw, err := json.Marshal(map[string]any{"type": "custom", "id": state.leaf, "parentId": nil, "customType": "identity", "data": nil})
					if err != nil {
						t.Fatal(err)
					}
					// Keep the reference in memory; the selected path need not exist for identity reads.
					reference.SetPath("")
					if err := reference.AppendEntry(json.RawMessage(raw)); err != nil {
						t.Fatal(err)
					}
					reference.SetPath(state.file)
				}
				// Pi's getSessionFile is `string | undefined` and getLeafId is `string | null`: an in-memory or empty session is absent (JSON null), not an empty string.
				leaf := ""
				if value := reference.GetLeafID(); value != nil {
					leaf = *value
				}
				id := reference.GetSessionId()
				want := []*string{&id, nilIfEmpty(reference.Path()), nilIfEmpty(leaf), nilIfEmpty(reference.GetSessionName())}
				if h.bridge != nil {
					h.bridge.SetHostAction("getSessionID", reference.ID)
					h.bridge.SetHostAction("getSessionFile", reference.Path)
					h.bridge.SetHostAction("getLeafID", func() string { return leaf })
					h.bridge.SetHostAction("getSessionName", reference.GetSessionName)
				} else {
					h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{SessionManager: reference}, nil)
				}
				command, ok := findCommand(h.runner, "session-identity")
				if !ok {
					t.Fatal("session-identity command missing")
				}
				*h.notify = nil
				if err := command.Handler(h.runner.DispatchContext(t.Context()), ""); err != nil {
					t.Fatal(err)
				}
				if len(*h.notify) != 1 {
					t.Fatalf("notifications = %v", *h.notify)
				}
				var got []*string
				if err := json.Unmarshal([]byte(strings.TrimSuffix((*h.notify)[0], ":info")), &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("identity = %s, want %s", showOptionalStrings(got), showOptionalStrings(want))
				}
				if state.file == "" && (got[1] != nil || got[2] != nil || got[3] != nil) {
					t.Errorf("in-memory session file, leaf and name = %s, want all absent", showOptionalStrings(got))
				}
				if state.name != "" && (got[3] == nil || *got[3] != state.name) {
					t.Errorf("session name = %s, want %q", showOptionalStrings(got), state.name)
				}
			}
		})
	}
}

func nilIfEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func showOptionalStrings(values []*string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
