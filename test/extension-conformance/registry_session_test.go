package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func registryProbeView() codingagent.ExtensionSessionView {
	s := codingagent.NewSession("registry-session", "/conformance/registry")
	for _, raw := range []string{
		`{"type":"custom_message","id":"one","parentId":null,"timestamp":"1970-01-01T00:00:00.123456789Z","customType":"probe","content":"hello","display":false}`,
		`{"type":"label","id":"two","parentId":"one","timestamp":"1970-01-01T00:00:01.000Z","targetId":"one","label":"chosen"}`,
	} {
		if err := s.AppendEntry(json.RawMessage(raw)); err != nil {
			panic(err)
		}
	}
	s.SetPath("session.jsonl")
	return codingagent.ExtensionSessionView{Session: s, CWD: "/conformance/registry", SessionDir: "relative/sessions"}
}

func registryProbeState() map[string]any {
	return map[string]any{
		"models":    []any{map[string]any{"id": "model", "modelId": "model", "provider": "registry-probe", "name": "Model", "displayName": "Model", "api": "openai-completions"}},
		"providers": map[string]any{"registry-probe": map[string]any{"name": "Registry Probe", "configured": true, "usingOAuth": true, "authStatus": map[string]any{"configured": true, "source": "stored"}}},
		"error":     "probe-error",
	}
}
func registryProbeAuth() map[string]any {
	return map[string]any{"auth": map[string]any{"apiKey": "probe-key"}, "source": "stored credential"}
}
func registryProbeRefresh() map[string]any {
	return map[string]any{"aborted": true, "errors": map[string]string{"registry-probe": "probe-refresh-error"}}
}

// The reference drives the same pure host projection as the production sessionRead
// binding. The paired Pi scenario independently checks those projection algorithms.
func registryProbeReference() (map[string]any, error) {
	view := registryProbeView()
	out := map[string]any{}
	for key, method := range map[string]string{"cwd": "getCwd", "dir": "getSessionDir", "id": "getSessionId", "name": "getSessionName", "leaf": "getLeafId", "entry": "getEntry", "missing": "getEntry", "label": "getLabel", "entries": "getEntries", "branch": "getBranch", "tree": "getTree", "contextEntries": "buildContextEntries", "projection": "buildSessionProjection"} {
		args := json.RawMessage(`{"id":"one","fromId":"one"}`)
		if key == "missing" {
			args = json.RawMessage(`{"id":"missing"}`)
		}
		value, err := codingagent.ExtensionSessionRead(view, method, args)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	state := registryProbeState()
	out["models"] = state["models"]
	out["available"] = state["models"]
	out["status"] = map[string]any{"configured": true, "source": "stored"}
	out["display"] = "Registry Probe"
	out["error"] = "probe-error"
	out["config"] = map[string]any{"apiKey": "probe-key", "name": "Registry Probe"}
	out["ids"] = []string{"registry-probe"}
	out["auth"] = registryProbeAuth()
	out["apiKey"] = "probe-key"
	out["missingKey"] = nil
	out["refresh"] = registryProbeRefresh()
	return out, nil
}

func TestRegistrySessionFacadesAcrossSDKs(t *testing.T) {
	expected, err := registryProbeReference()
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(expectedJSON, &want); err != nil {
		t.Fatal(err)
	}
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
			if h.bridge != nil {
				view := registryProbeView()
				path := filepath.Join(t.TempDir(), "session.jsonl")
				header, err := json.Marshal(view.Session.GetHeader())
				if err != nil {
					t.Fatal(err)
				}
				data := header
				data = append(data, '\n')
				for _, entry := range view.Session.GetEntries() {
					data = append(data, entry.Raw()...)
					data = append(data, '\n')
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				view.Session.SetPath(path)
				b := h.bridge
				b.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
					return codingagent.ExtensionSessionRead(view, method, args)
				})
				b.SetHostAction("getSessionID", func() string { return view.Session.ID() })
				b.SetHostAction("getSessionName", func() string { return "" })
				b.SetHostAction("getSessionFile", func() string { return path })
				b.SetHostAction("getLeafID", func() string { return "two" })
				b.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
					entries := view.Session.GetEntries()
					if cursor < 0 || cursor > len(entries) {
						cursor = 0
					}
					page := []json.RawMessage{}
					for _, entry := range entries[cursor:] {
						page = append(page, entry.Raw())
					}
					return page, len(entries), false, "two"
				})
				b.ForgetProviderRegistration("conformance-oauth")
				b.ForgetProviderRegistration("conformance-oauth-object")
				b.ForgetProviderRegistration("conformance-oauth-large")
				b.SetHostAction("getModelRegistryState", registryProbeState)
				b.SetHostAction("getProviderAuth", func(_ context.Context, provider string) (map[string]any, error) {
					if provider == "missing" {
						return nil, errors.New("no auth")
					}
					return registryProbeAuth(), nil
				})
				b.SetHostAction("refreshModelRegistry", func(_ context.Context, network *bool, _ []string, _ *bool) (map[string]any, error) {
					if network == nil || *network {
						return nil, errors.New("network option not propagated")
					}
					return registryProbeRefresh(), nil
				})
				b.RecordProviderRegistration("registry-probe", json.RawMessage(`{"apiKey":"probe-key","name":"Registry Probe"}`))
				h.host.BroadcastStateUpdate()
			}
			command, ok := findCommand(h.runner, "registry-session")
			if !ok {
				t.Fatal("missing registry-session command")
			}
			*h.notify = nil
			if err := command.Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			if len(*h.notify) != 1 {
				t.Fatalf("notifications: %v", *h.notify)
			}
			var got any
			// recordingUI appends the notification level.
			text := strings.TrimSuffix((*h.notify)[0], ":info")
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatalf("decode %q: %v", text, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s\nwant %s", text, expectedJSON)
			}
		})
	}
}
