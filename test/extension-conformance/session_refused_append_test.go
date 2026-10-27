package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// upstream: packages/coding-agent/src/core/session-manager.ts appendCustomEntry (pushes the entry on the log, sets byId and moves the leaf) and agent-session.ts:3406-3411 (pi.appendEntry calls it): in Pi an append is in-process and always lands, so the session an extension reads is the session the host holds.
//
// Invariant: what an extension's session reads show is the host's session. An append the host refuses is in no session and no read shows it; an append the host takes is read with the parent the host linked it to. A Node extension shows its own append before the host has answered, as Pi's in-process log does, so it is the SDK that can break the invariant: it kept the entry the host refused and the parents it had assumed.
//
// Every SDK appends four entries in a handler: one the host refuses, one it takes, one it takes after a native message it appended meanwhile, and a second refused. Afterwards every SDK must read exactly the host's log: the entry count, and the branch walked from the leaf by parent links. The counts and ids are the host's own, which no SDK's empty fallback or unmade call produces, and the host records that it received the four appends in order.
func TestSessionMirrorHoldsOnlyWhatTheHostAppendedAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	message := func(id, parent string) string {
		parentJSON := "null"
		if parent != "" {
			parentJSON = fmt.Sprintf("%q", parent)
		}
		return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-10-11T00:00:00.000Z","message":{"role":"user","content":"text of %s"}}`, id, parentJSON, id)
	}
	const appends = "conformance-refused,conformance-taken,conformance-raced,conformance-refused"
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
			session := codingagent.NewSession("refused-append", "/w")
			session.SetPath(t.TempDir() + "/session.jsonl")
			for _, raw := range []string{message("s1", ""), message("s2", "s1"), message("s3", "s2")} {
				if err := session.AppendEntry(json.RawMessage(raw)); err != nil {
					t.Fatal(err)
				}
			}
			view := codingagent.ExtensionSessionView{Session: session, CWD: "/w"}
			leaf := func() string {
				if id := session.GetLeafID(); id != nil {
					return *id
				}
				return ""
			}
			h.bridge.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
				return codingagent.ExtensionSessionRead(view, method, args)
			})
			h.bridge.SetHostAction("getSessionID", func() string { return session.ID() })
			h.bridge.SetHostAction("getSessionName", func() string { return "" })
			h.bridge.SetHostAction("getSessionFile", func() string { return session.Path() })
			h.bridge.SetHostAction("getLeafID", leaf)
			h.bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
				all := session.GetEntries()
				if cursor < 0 || cursor > len(all) {
					cursor = 0
				}
				page := []json.RawMessage{}
				for _, entry := range all[cursor:] {
					page = append(page, entry.Raw())
				}
				return page, len(all), false, leaf()
			})
			var (
				mu       sync.Mutex
				received []string
			)
			// The host action every mode binds is codingagent.BindAppendEntryAction over a Session; this one refuses a type and appends a native message before one, then takes the rest through the same Session write.
			h.bridge.SetHostAction("appendEntry", func(customType string, data any, direct *subprocess.DirectEntryAppend) error {
				mu.Lock()
				defer mu.Unlock()
				received = append(received, fmt.Sprintf("%s=%v", customType, data))
				switch customType {
				case "conformance-refused":
					return errors.New("conformance host refusal")
				case "conformance-raced":
					if err := session.AppendEntry(json.RawMessage(message("native-1", leaf()))); err != nil {
						return err
					}
				}
				_, err := codingagent.AppendExtensionEntry(session, customType, data, direct)
				return err
			})
			h.host.BroadcastStateUpdate()

			run := func(name, args string) string {
				t.Helper()
				command, ok := findCommand(h.runner, name)
				if !ok {
					t.Fatalf("missing %s command", name)
				}
				*h.notify = nil
				if err := command.Handler(context.Background(), args); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if len(*h.notify) != 1 {
					t.Fatalf("%s: notifications %v", name, *h.notify)
				}
				return strings.TrimSuffix((*h.notify)[0], ":info")
			}
			*h.notify = nil
			command, ok := findCommand(h.runner, "append-entries-probe")
			if !ok {
				t.Fatal("missing append-entries-probe command")
			}
			if err := command.Handler(context.Background(), appends); err != nil {
				t.Fatalf("append-entries-probe: %v", err)
			}

			mu.Lock()
			got := slices.Clone(received)
			mu.Unlock()
			wantCalls := []string{
				"conformance-refused=data-of-conformance-refused",
				"conformance-taken=data-of-conformance-taken",
				"conformance-raced=data-of-conformance-raced",
				"conformance-refused=data-of-conformance-refused",
			}
			if !slices.Equal(got, wantCalls) {
				t.Fatalf("the host received appends %v, want %v", got, wantCalls)
			}
			var ids, customTypes []string
			for _, entry := range session.GetEntries() {
				var record struct {
					ID         string `json:"id"`
					CustomType string `json:"customType"`
				}
				if err := json.Unmarshal(entry.Raw(), &record); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, record.ID)
				if record.CustomType != "" {
					customTypes = append(customTypes, record.CustomType)
				}
			}
			if want := []string{"conformance-taken", "conformance-raced"}; !slices.Equal(customTypes, want) {
				t.Fatalf("the host's custom entries are %v, want %v", customTypes, want)
			}
			if len(ids) != 6 || ids[4] != "native-1" {
				t.Fatalf("the host's log is %v, want s1 s2 s3, the taken entry, native-1 and the raced entry", ids)
			}

			// Each read below is a host-synced call after the refusals, as the issue's two reads are.
			for round := range 2 {
				if mirror, want := run("session-log-probe", ""), fmt.Sprintf("session entries=%d branch=%d", len(ids), len(ids)); mirror != want {
					t.Fatalf("read %d: the mirror holds %q, the host's log has %d entries (%q)", round, mirror, len(ids), want)
				}
				want := fmt.Sprintf("branch=%s leaf=%s entry=none label=none", strings.Join(ids, ">"), ids[len(ids)-1])
				if mirror := run("session-branch-probe", "conformance-no-such-entry"); mirror != want {
					t.Fatalf("read %d: the mirror reads\n got  %s\n want %s", round, mirror, want)
				}
			}
		})
	}
}
