package coding_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func newEntryAppendSession(t *testing.T, options coding.SessionOptions) *coding.Session {
	t.Helper()
	root := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: root, AgentDir: filepath.Join(root, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	options.ResourceLoader = coding.NoResources
	options.SkipBuiltinTools = true
	session, err := coding.NewSession(services, options)
	if err != nil {
		t.Fatal(err)
	}
	session.DiscardEvents()
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	flushAppendEvents(t, session)
	return session
}

func flushAppendEvents(t *testing.T, session *coding.Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := session.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAppendExtensionEntryPreservesIdentityAndEventSemantics(t *testing.T) {
	for _, event := range []bool{false, true} {
		t.Run(fmt.Sprintf("event=%t", event), func(t *testing.T) {
			session := newEntryAppendSession(t, coding.SessionOptions{NoSession: true})
			parent := session.LeafID()
			direct := &subprocess.DirectEntryAppend{ID: "a1b2c3d4", Timestamp: "2026-10-01T12:34:56.123456789+02:00", Event: event}
			var events []json.RawMessage
			stop := session.Subscribe(func(ev agent.AgentEvent) {
				if appended, ok := ev.(agent.EntryAppendedEvent); ok {
					events = append(events, appended.Entry)
				}
			})
			defer stop()
			id, err := session.AppendExtensionEntry("fixture", map[string]string{"text": "retained"}, direct)
			if err != nil || id != direct.ID {
				t.Fatalf("append = %q, %v, want %q", id, err, direct.ID)
			}
			entry, ok := session.SessionManager().GetEntry(id)
			if !ok || entry.Base().Timestamp != direct.Timestamp || !reflect.DeepEqual(entry.Base().ParentID, parent) {
				t.Fatalf("entry = %+v, found = %t; want the supplied identity on the active branch", entry, ok)
			}
			flushAppendEvents(t, session)
			if event {
				if len(events) != 1 || !bytes.Equal(events[0], entry.Raw()) {
					t.Fatalf("events = %s, want the committed entry %s", events, entry.Raw())
				}
			} else if len(events) != 0 {
				t.Fatalf("direct session-manager append emitted %d events", len(events))
			}
			allocated, err := session.AppendCustomEntry("allocated", nil)
			if err != nil || allocated == "" || allocated == id {
				t.Fatalf("host-assigned append = %q, %v", allocated, err)
			}
			flushAppendEvents(t, session)
			if len(events) == 0 {
				t.Fatal("host-assigned append emitted no event")
			}
			if last := events[len(events)-1]; !bytes.Contains(last, []byte(allocated)) {
				t.Fatalf("host-assigned append event = %s, want ID %q", last, allocated)
			}
		})
	}
}

func TestAppendExtensionEntryRejectsWithoutChangingTheLog(t *testing.T) {
	session := newEntryAppendSession(t, coding.SessionOptions{NoSession: true})
	seed, err := session.AppendCustomEntry("seed", "retained")
	if err != nil {
		t.Fatal(err)
	}
	flushAppendEvents(t, session)
	before, _ := session.SessionManager().GetEntry(seed)
	count := session.SessionManager().GetEntryCount()
	events := 0
	stop := session.Subscribe(func(ev agent.AgentEvent) {
		if _, ok := ev.(agent.EntryAppendedEvent); ok {
			events++
		}
	})
	defer stop()
	for _, tc := range []struct {
		name   string
		direct subprocess.DirectEntryAppend
		data   any
	}{
		{"duplicate", subprocess.DirectEntryAppend{ID: seed, Timestamp: "2026-10-01T00:00:00Z", Event: true}, "replacement"},
		{"empty ID", subprocess.DirectEntryAppend{Timestamp: "2026-10-01T00:00:00Z", Event: true}, nil},
		{"invalid timestamp", subprocess.DirectEntryAppend{ID: "invalid", Timestamp: "yesterday", Event: true}, nil},
		{"encoding failure", subprocess.DirectEntryAppend{ID: "encoding", Timestamp: "2026-10-01T00:00:00Z", Event: true}, func() {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if id, err := session.AppendExtensionEntry("rejected", tc.data, &tc.direct); err == nil || id != "" {
				t.Fatalf("rejected append = %q, %v", id, err)
			}
			flushAppendEvents(t, session)
			retained, _ := session.SessionManager().GetEntry(seed)
			if session.SessionManager().GetEntryCount() != count || *session.LeafID() != seed || !bytes.Equal(retained.Raw(), before.Raw()) || events != 0 {
				t.Fatal("a rejected append changed the log, leaf or events")
			}
		})
	}
}

func TestAppendExtensionEntrySharesTheNativeAppendLock(t *testing.T) {
	session := newEntryAppendSession(t, coding.SessionOptions{NoSession: true})
	before := session.SessionManager().GetEntryCount()
	parent := session.LeafID()
	const writers = 64
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			<-start
			var err error
			if i%2 == 0 {
				_, err = session.AppendExtensionEntry("extension", i, &subprocess.DirectEntryAppend{ID: fmt.Sprintf("entry-%d", i), Timestamp: "2026-10-01T00:00:00Z"})
			} else {
				_, err = session.SessionManager().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("native"), Timestamp: int64(i + 1)}})
			}
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries := session.Entries()[before:]
	if len(entries) != writers {
		t.Fatalf("retained %d entries for %d writers", len(entries), writers)
	}
	for _, entry := range entries {
		base := entry.Base()
		if !reflect.DeepEqual(base.ParentID, parent) {
			t.Fatalf("entry %q has parent %v, want %v", base.ID, base.ParentID, parent)
		}
		parent = &base.ID
	}
	if !reflect.DeepEqual(session.LeafID(), parent) {
		t.Fatal("the final leaf is not the last appended entry")
	}
}

func TestAppendExtensionEntryPersistsIdentityAcrossReopen(t *testing.T) {
	session := newEntryAppendSession(t, coding.SessionOptions{})
	if _, err := session.SessionManager().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("persist"), Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	direct := &subprocess.DirectEntryAppend{ID: "retained", Timestamp: "2026-10-01T12:34:56.123456789+02:00", Event: true}
	if _, err := session.AppendExtensionEntry("fixture", "persisted", direct); err != nil {
		t.Fatal(err)
	}
	entry, _ := session.SessionManager().GetEntry(direct.ID)
	path := session.Path()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := newEntryAppendSession(t, coding.SessionOptions{ResumePath: path})
	retained, ok := reopened.SessionManager().GetEntry(direct.ID)
	if !ok || !bytes.Equal(retained.Raw(), entry.Raw()) {
		t.Fatalf("reopened entry = %+v, found = %t; want %s", retained, ok, entry.Raw())
	}
}

func TestAppendExtensionEntryPropagatesNativeStorageFailure(t *testing.T) {
	session := newEntryAppendSession(t, coding.SessionOptions{})
	if _, err := session.SessionManager().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("persist"), Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	path := session.Path()
	backup := path + ".saved"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	restore := sync.OnceFunc(func() {
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
		if err := os.Rename(backup, path); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(restore)
	events := 0
	stop := session.Subscribe(func(ev agent.AgentEvent) {
		if _, ok := ev.(agent.EntryAppendedEvent); ok {
			events++
		}
	})
	defer stop()
	direct := &subprocess.DirectEntryAppend{ID: "not-persisted", Timestamp: "2026-10-01T00:00:00Z", Event: true}
	id, err := session.AppendExtensionEntry("fixture", "failed write", direct)
	var pathError *os.PathError
	if id != "" || !errors.As(err, &pathError) {
		t.Fatalf("append = %q, %v; want the native filesystem error", id, err)
	}
	flushAppendEvents(t, session)
	if events != 0 {
		t.Fatalf("failed write emitted %d entry_appended events", events)
	}
	// Pi's native writer updates memory before persistence. The public wrapper reports the error without introducing rollback semantics.
	if _, ok := session.SessionManager().GetEntry(direct.ID); !ok {
		t.Fatal("the wrapper changed the native writer's in-memory failure semantics")
	}
	restore()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := newEntryAppendSession(t, coding.SessionOptions{ResumePath: path})
	if _, ok := reopened.SessionManager().GetEntry(direct.ID); ok {
		t.Fatal("the failed write appeared after reopening the persisted session")
	}
}

const extensionEntryFixture = `export default function (pi) {
  const seen = [];
  pi.registerTool({ name: "append_probe", label: "Append", description: "Append a custom entry", parameters: { type: "object", properties: { event: { type: "boolean" } } },
    async execute(_id, args, _signal, _update, ctx) {
      if (args.event) pi.appendEntry("fixture.event", { text: "emitted" });
      else ctx.sessionManager.appendCustomEntry("fixture.silent", { text: "silent" });
      const entry = ctx.sessionManager.getLeafEntry();
      seen.push({ id: entry.id, timestamp: entry.timestamp });
      return { content: [{ type: "text", text: "appended" }], details: {} };
    } });
  pi.registerTool({ name: "read_probe", label: "Read", description: "Read retained entries", parameters: { type: "object" },
    async execute(_id, _args, _signal, _update, ctx) {
      const entries = ctx.sessionManager.getEntries().filter(e => e.customType?.startsWith("fixture."));
      return { content: [{ type: "text", text: JSON.stringify({ seen, entries }) }], details: {} };
    } });
}`

// agent-session.ts _bindExtensionCore emits entry_appended for pi.appendEntry; session-manager.ts appendCustomEntry writes only the log. A subprocess host must keep the identity already returned synchronously in the extension process.
func TestAppendExtensionEntryThroughNodeHost(t *testing.T) {
	for _, isolation := range []string{"isolated", "packed"} {
		t.Run(isolation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			session := newEntryAppendSession(t, coding.SessionOptions{NoSession: true})
			bridge := subprocess.NewUIBridge(nil)
			coding.BindSessionReadActions(bridge, func() *coding.Session { return session }, session.CWD(), "")
			bridge.SetHostAction("appendEntry", func(customType string, data any, direct *subprocess.DirectEntryAppend) error {
				_, err := session.AppendExtensionEntry(customType, data, direct)
				return err
			})
			root := t.TempDir()
			source := filepath.Join(root, "entry.mjs")
			if err := os.WriteFile(source, []byte(extensionEntryFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			host := subprocess.NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			host.SetUIBridge(bridge)
			defer host.Shutdown("test complete")
			config := subprocess.ExtConfig{Name: "entry", Source: source, Enabled: true}
			if isolation == "isolated" {
				config.Isolation = "isolated"
			}
			loaded, errs := host.LoadAll(ctx, []subprocess.ExtConfig{config})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("loaded %d extensions, errors = %v", len(loaded), errs)
			}
			for _, args := range []string{`{"event":true}`, `{"event":false}`} {
				if _, err := loaded[0].Tools["append_probe"].Definition.Execute(ctx, "append", json.RawMessage(args), nil); err != nil {
					t.Fatal(err)
				}
			}
			result, err := loaded[0].Tools["read_probe"].Definition.Execute(ctx, "read", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			var view struct {
				Seen    []struct{ ID, Timestamp string }
				Entries []json.RawMessage
			}
			if len(result.Content) != 1 {
				t.Fatalf("read returned %d content blocks, want one", len(result.Content))
			}
			content, ok := result.Content[0].(ai.TextContent)
			if !ok {
				t.Fatalf("read content = %T, want text", result.Content[0])
			}
			if err := json.Unmarshal([]byte(content.Text), &view); err != nil {
				t.Fatal(err)
			}
			if len(view.Seen) != 2 || len(view.Entries) != len(view.Seen) {
				t.Fatalf("extension view = %s, want both appends exactly once", content.Text)
			}
			for i, seen := range view.Seen {
				entry, ok := session.SessionManager().GetEntry(seen.ID)
				if !ok || entry.Base().Timestamp != seen.Timestamp || !bytes.Equal(entry.Raw(), view.Entries[i]) {
					t.Fatalf("extension identity %+v and mirror %s disagree with the host entry %+v (found=%t)", seen, view.Entries[i], entry, ok)
				}
			}
		})
	}
}
