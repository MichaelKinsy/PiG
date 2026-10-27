package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// replicatedHostSession is the Session behind the replicated-accessors row: a real codingagent.Session log that the write actions append to, the way Pi's
// synchronous Session does (agent-session.ts setSessionName, sendCustomMessage; sessionManager.appendLabelChange), and a model that setModel replaces. The
// session name and the entries are read through the production sessionRead projection, so every SDK (Node adopts the leaf entry a write appended) sees what
// the real host would. Its values are sentinels no SDK fallback produces.
type replicatedHostSession struct {
	mu      sync.Mutex
	session *codingagent.Session
	model   string
	seq     int
}

func replicatedModelJSON(provider, id, name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"provider":%q,"api":"openai-completions"}`, id, name, provider)
}

func (s *replicatedHostSession) view() codingagent.ExtensionSessionView {
	return codingagent.ExtensionSessionView{Session: s.session, CWD: "/conformance/replicated", SessionDir: "relative/sessions"}
}

func (s *replicatedHostSession) read(method string) any {
	value, err := codingagent.ExtensionSessionRead(s.view(), method, nil)
	if err != nil {
		panic(err)
	}
	return value
}

func (s *replicatedHostSession) leaf() string {
	if leaf := s.session.GetLeafID(); leaf != nil {
		return *leaf
	}
	return ""
}

// appendEntry appends one entry under the current leaf; the caller holds mu.
func (s *replicatedHostSession) appendEntry(entryType, fields string) {
	s.seq++
	parent := "null"
	if leaf := s.leaf(); leaf != "" {
		parent = fmt.Sprintf("%q", leaf)
	}
	raw := fmt.Sprintf(`{"type":%q,"id":"replicated-%d","parentId":%s,"timestamp":"1970-01-01T00:00:%02d.000Z",%s}`, entryType, s.seq, parent, s.seq, fields)
	if err := s.session.AppendEntry(json.RawMessage(raw)); err != nil {
		panic(err)
	}
}

func (s *replicatedHostSession) bind(b *subprocess.UIBridge) {
	b.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return codingagent.ExtensionSessionRead(s.view(), method, args)
	})
	b.SetHostAction("getSessionID", func() string { return s.session.ID() })
	b.SetHostAction("getSessionFile", func() string { return "" })
	b.SetHostAction("getSessionName", func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		name, _ := s.read("getSessionName").(string)
		return name
	})
	b.SetHostAction("getLeafID", func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.leaf()
	})
	b.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		entries := s.session.GetEntries()
		if cursor < 0 || cursor > len(entries) {
			cursor = 0
		}
		page := []json.RawMessage{}
		for _, entry := range entries[cursor:] {
			page = append(page, entry.Raw())
		}
		return page, len(entries), false, s.leaf()
	})
	b.SetHostAction("getModelInfo", func() json.RawMessage {
		s.mu.Lock()
		defer s.mu.Unlock()
		return json.RawMessage(s.model)
	})
	b.SetHostAction("setSessionName", func(name string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.appendEntry("session_info", fmt.Sprintf(`"name":%q`, name))
		return nil
	})
	b.SetHostAction("setLabel", func(entryID, label string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.appendEntry("label", fmt.Sprintf(`"targetId":%q,"label":%q`, entryID, label))
		return nil
	})
	b.SetHostAction("sendMessage", func(message extension.CustomMessageRef, _ subprocess.SendMessageOptions) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.appendEntry("custom_message", fmt.Sprintf(`"customType":%q,"content":%q,"display":%t`, message.CustomType, message.Content, message.Display))
		return nil
	})
	b.SetHostAction("setModel", func(_ context.Context, model string) (bool, error) {
		provider, id, ok := strings.Cut(model, "/")
		if !ok {
			return false, fmt.Errorf("model %q is not provider/id", model)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.model = replicatedModelJSON(provider, id, "Name After")
		return true, nil
	})
}

// Pi's ctx.model, ctx.sessionManager.getSessionName() and ctx.sessionManager.getEntries() are live, and its Session writes are synchronous, so a read
// right after setSessionName, setLabel, sendMessage or setModel returns the written value (agent-session.ts; runner.ts:906). Every SDK reads the same
// identity: the model id, never its display name (Python ctx.model and Rust ctx.model() returned "Name Sentinel"), and the session name (Python
// ctx.session_name and Rust ctx.session_name() returned "" because the host never filled the ready payload's name). The initial values are sentinels
// that no SDK defaults to, and the writes move each to a second sentinel, so an SDK that never reads the host, or reads it one push late, fails.
func TestReplicatedAccessorsReadYourWritesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	want := map[string]any{
		"name0": "s-sentinel", "model0": "id-sentinel", "provider0": "prov-sentinel",
		"name1": "renamed", "entryAfterLabel": "label", "entryAfterMessage": "custom_message",
		"model1": "id-after", "provider1": "prov-after", "qualified1": "prov-after/id-after",
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			session := &replicatedHostSession{session: codingagent.NewSession("replicated-session", "/conformance/replicated"), model: replicatedModelJSON("prov-sentinel", "id-sentinel", "Name Sentinel")}
			// The log starts with a named session and a custom entry, so the last entry's type is neither "label" nor "custom_message" before a write.
			session.appendEntry("session_info", `"name":"s-sentinel"`)
			session.appendEntry("custom", `"customType":"probe","data":"hello"`)
			session.bind(h.bridge)
			h.host.BroadcastStateUpdate()
			command, ok := findCommand(h.runner, "replicated-accessors")
			if !ok {
				t.Fatal("replicated-accessors command missing")
			}
			*h.notify = nil
			if err := command.Handler(h.runner.DispatchContext(t.Context()), ""); err != nil {
				t.Fatal(err)
			}
			if len(*h.notify) != 1 {
				t.Fatalf("notifications = %v", *h.notify)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSuffix((*h.notify)[0], ":info")), &got); err != nil {
				t.Fatalf("decode %q: %v", (*h.notify)[0], err)
			}
			for key, expected := range want {
				if got[key] != expected {
					t.Errorf("%s = %v, want %v (all: %v)", key, got[key], expected, got)
				}
			}
		})
	}
}
