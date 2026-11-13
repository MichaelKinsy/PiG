package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// wantEntryNullMemberCalls are the session file's lines for the four calls every SDK's entry_null_members command makes, with identities and times replaced,
// and what a reader of the reloaded file gets for the optional member: an entry without data and one whose data is null (pi.appendEntry), a message without
// details and one whose details are null (pi.sendMessage). The lines are the ones the pinned Pi package writes for the same calls
// (coding/testdata/entry-null-members-pi.jsonl, rows "extension appendEntry data ..." and "extension sendMessage details ...").
var wantEntryNullMemberCalls = []string{
	`{"type":"custom","customType":"conformance-null-explicit","data":null,"id":"#","parentId":"#","timestamp":"T"} => data=null`,
	`{"type":"custom","customType":"conformance-null-omitted","id":"#","parentId":"#","timestamp":"T"} => data=undefined`,
	`{"type":"custom_message","customType":"conformance-null-explicit","content":"x","display":false,"details":null,"id":"#","parentId":"#","timestamp":"T"} => details=null`,
	`{"type":"custom_message","customType":"conformance-null-omitted","content":"x","display":false,"id":"#","parentId":"#","timestamp":"T"} => details=undefined`,
}

// wantEntryNullMemberDrafts are the lines for the drafts every SDK's agent_before_settle handler returns, applied as agent-session.ts _applyBoundaryDrafts
// applies them: a custom entry without data, a compaction with details and a null usage, a custom entry with a null data and a message with null details.
var wantEntryNullMemberDrafts = []string{
	`{"type":"custom","customType":"conformance-boundary","id":"#","parentId":"#","timestamp":"T"} => data=undefined`,
	`{"type":"compaction","id":"#","parentId":"#","timestamp":"T","summary":"conformance-summary","firstKeptEntryId":"#","tokensBefore":1,"details":{"format":"conformance-checkpoint"},"usage":null,"fromHook":true} => details=object,usage=null`,
	`{"type":"custom","customType":"conformance-null-data","data":null,"id":"#","parentId":"#","timestamp":"T"} => data=null`,
	`{"type":"custom_message","customType":"conformance-null-details","content":"x","display":false,"details":null,"id":"#","parentId":"#","timestamp":"T"} => details=null`,
}

var entryNullMemberIdentity = regexp.MustCompile(`"(id|parentId|firstKeptEntryId)":"[0-9a-f]{8}"`)
var entryNullMemberTime = regexp.MustCompile(`"timestamp":"\d{4}-[^"]*Z"`)

// entryNullMemberLines are the session file's lines after the two messages that create it, each with what a reader of the reloaded file gets for the optional
// members of its entry type.
func entryNullMemberLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := icodingagent.LoadEntriesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != len(entries) {
		t.Fatalf("the file has %d lines and reloads as %d entries", len(lines), len(entries))
	}
	var out []string
	for i, line := range lines[3:] {
		var members map[string]json.RawMessage
		if err := json.Unmarshal(entries[i+3].Raw(), &members); err != nil {
			t.Fatal(err)
		}
		names := map[string][]string{`"custom"`: {"data"}, `"custom_message"`: {"details"}, `"compaction"`: {"details", "usage"}, `"label"`: {"label"}}[string(members["type"])]
		states := make([]string, 0, len(names))
		for _, name := range names {
			state := "undefined"
			if value, present := members[name]; present {
				state = map[byte]string{'n': "null", '{': "object", '"': "string"}[value[0]]
			}
			states = append(states, name+"="+state)
		}
		line = entryNullMemberTime.ReplaceAllString(entryNullMemberIdentity.ReplaceAllString(line, `"${1}":"#"`), `"timestamp":"T"`)
		out = append(out, line+" => "+strings.Join(states, ","))
	}
	return out
}

// Pi: packages/coding-agent/src/core/session-manager.ts:1283-1312 (appendCompaction, appendCustomEntry), :1350-1366 (appendCustomMessageEntry) store each member
// as given, and JSON.stringify writes a null and leaves out an undefined one; packages/coding-agent/src/core/agent-session.ts:3562 (appendEntry), :1217 and
// :2499 (a custom message's details), :1007-1045 (_applyBoundaryDrafts). An extension says "no value" and "null" in its own language: Node undefined and null,
// Go nil and json.RawMessage("null"), Python None and pig_sdk.NULL, Rust Value::Null or None and Some(Value::Null), Lisp NIL and :NULL. The row binds both to
// the session FILE's line and to the reloaded value, so an SDK that drops the null or writes one for an omitted member fails, isolated, packed and fused.
func TestEntryMemberNullAndOmittedAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, test := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("entry null members complete")
				}
			})
			dir := t.TempDir()
			session, err := icodingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range []string{
				`{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.001Z","message":{"role":"user","content":"u","timestamp":1}}`,
				`{"type":"message","id":"00000002","parentId":"00000001","timestamp":"2026-01-01T00:00:00.002Z","message":{"role":"assistant","content":[{"type":"text","text":"a"}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`,
			} {
				if err := session.AppendEntry(json.RawMessage(line)); err != nil {
					t.Fatal(err)
				}
			}
			appendEntry := func(customType string, data any) error {
				_, err := icodingagent.AppendExtensionEntry(session, customType, data, nil)
				return err
			}
			sendMessage := func(message extension.CustomMessageRef) error {
				_, err := session.AppendCustomMessageEntry(message.CustomType, message.Content, message.Display, message.Details)
				return err
			}
			if test.name != "inproc-go" {
				if h.bridge == nil {
					t.Fatal("the harness has no host bridge to bind the session's writers on")
				}
				// The session's writers behind the host actions, as a mode binds them (agent-session.ts _bindExtensionCore).
				h.bridge.SetHostAction("appendEntry", func(customType string, data any, _ *subprocess.DirectEntryAppend) error {
					return appendEntry(customType, data)
				})
				h.bridge.SetHostAction("sendMessage", func(message extension.CustomMessageRef, _ subprocess.SendMessageOptions) error {
					return sendMessage(message)
				})
				command, ok := findCommand(h.runner, "entry_null_members")
				if !ok {
					t.Fatal("entry_null_members command not registered")
				}
				if err := command.Handler(t.Context(), ""); err != nil {
					t.Fatalf("entry_null_members command: %v", err)
				}
				waitFor(t, func() bool { return len(session.GetEntries()) >= 6 })
			} else {
				// The in-process reference: the session's own API, where a nil `any` is the omitted argument and json.RawMessage("null") is null.
				for _, call := range []func() error{
					func() error { return appendEntry("conformance-null-omitted", nil) },
					func() error { return appendEntry("conformance-null-explicit", json.RawMessage("null")) },
					func() error {
						return sendMessage(extension.CustomMessageRef{CustomType: "conformance-null-omitted", Content: "x"})
					},
					func() error {
						return sendMessage(extension.CustomMessageRef{CustomType: "conformance-null-explicit", Content: "x", Details: json.RawMessage("null")})
					},
				} {
					if err := call(); err != nil {
						t.Fatal(err)
					}
				}
			}
			calls := entryNullMemberLines(t, session.Path())
			sort.Strings(calls)
			if got, want := strings.Join(calls, "\n"), strings.Join(wantEntryNullMemberCalls, "\n"); got != want {
				t.Errorf("the calls left the lines\n%s\nwant\n%s", got, want)
			}

			boundary, err := h.runner.EmitBoundary(t.Context(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", Outcome: extension.AgentActivityCompleted},
				func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
					return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries)), CanContinue: len(entries) > 0}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			for _, draft := range boundary.Entries {
				switch draft.Type {
				case extension.SessionBoundaryDraftCustom:
					_, err = session.AppendCustomEntry(draft.CustomType, draft.Data)
				case extension.SessionBoundaryDraftCustomMessage:
					_, err = session.AppendCustomMessageEntry(draft.CustomType, draft.Content, draft.Display, draft.Details)
				case extension.SessionBoundaryDraftCompaction:
					_, err = session.AppendCompactionEntry(icodingagent.CompactionEntry{Summary: draft.Summary, TokensBefore: 1, Details: draft.Details, Usage: draft.Usage, UsageNull: draft.UsageNull, UsageRaw: draft.UsageRaw, FromHook: true})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if got, want := strings.Join(entryNullMemberLines(t, session.Path())[4:], "\n"), strings.Join(wantEntryNullMemberDrafts, "\n"); got != want {
				t.Errorf("the drafts left the lines\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// wantLabelMemberCalls are the session file's lines for the label a reader can see cleared and for the three calls every SDK's label_members command
// makes, with identities and times replaced, and what a reader of the reloaded file gets for the label member. The lines are the ones the pinned Pi
// package writes for pi.setLabel(id), pi.setLabel(id, null) and pi.setLabel(id, "") (coding/testdata/entry-null-members-pi.jsonl, rows
// "extension setLabel ...").
var wantLabelMemberCalls = []string{
	`{"type":"label","id":"#","parentId":"#","timestamp":"T","targetId":"00000001","label":"mark"} => label=string`,
	`{"type":"label","id":"#","parentId":"#","timestamp":"T","targetId":"00000001"} => label=undefined`,
	`{"type":"label","id":"#","parentId":"#","timestamp":"T","targetId":"00000001","label":null} => label=null`,
	`{"type":"label","id":"#","parentId":"#","timestamp":"T","targetId":"00000001","label":""} => label=string`,
}

// Pi: packages/coding-agent/src/core/agent-session.ts:3575 (setLabel hands its label to appendLabelChange as given) and
// packages/coding-agent/src/core/session-manager.ts:1452-1464 (appendLabelChange stores it, and JSON.stringify writes a null, an empty string, and no
// member for undefined; `if (label)` clears the label for all three). An extension says the three in its own language: Node setLabel(id), null and "";
// Go SetLabelValue with nil and json.RawMessage("null"), and SetLabel with ""; Python set_label(id), pig_sdk.NULL and ""; Rust set_label_opt with None
// and Some(Value::Null), and set_label with ""; Lisp ctx-set-label without a label, :NULL and "". The row binds each to the session FILE's line, so an
// SDK that sends another form for one of them fails, isolated, packed and fused.
func TestSetLabelOmittedNullAndEmptyAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, test := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("label members complete")
				}
			})
			dir := t.TempDir()
			session, err := icodingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
			if err != nil {
				t.Fatal(err)
			}
			const target = "00000001"
			for _, line := range []string{
				`{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.001Z","message":{"role":"user","content":"u","timestamp":1}}`,
				`{"type":"message","id":"00000002","parentId":"00000001","timestamp":"2026-01-01T00:00:00.002Z","message":{"role":"assistant","content":[{"type":"text","text":"a"}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`,
			} {
				if err := session.AppendEntry(json.RawMessage(line)); err != nil {
					t.Fatal(err)
				}
			}
			// The session's writer behind the host action, as a mode binds it (agent-session.ts _bindExtensionCore).
			setLabel := func(entryID string, label *string, null bool) error {
				_, err := session.AppendLabelEntry(icodingagent.LabelEntry{TargetID: entryID, Label: label, LabelNull: null})
				return err
			}
			mark, empty := "mark", ""
			if err := setLabel(target, &mark, false); err != nil {
				t.Fatal(err)
			}
			if test.name != "inproc-go" {
				if h.bridge == nil {
					t.Fatal("the harness has no host bridge to bind the session's writer on")
				}
				h.bridge.SetHostAction("setLabel", setLabel)
				command, ok := findCommand(h.runner, "label_members")
				if !ok {
					t.Fatal("label_members command not registered")
				}
				if err := command.Handler(t.Context(), target); err != nil {
					t.Fatalf("label_members command: %v", err)
				}
				waitFor(t, func() bool { return len(session.GetEntries()) >= 6 })
			} else {
				// The in-process reference: the session's own API with the three forms.
				for _, call := range []func() error{
					func() error { return setLabel(target, nil, false) },
					func() error { return setLabel(target, nil, true) },
					func() error { return setLabel(target, &empty, false) },
				} {
					if err := call(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got, want := strings.Join(entryNullMemberLines(t, session.Path()), "\n"), strings.Join(wantLabelMemberCalls, "\n"); got != want {
				t.Errorf("the calls left the lines\n%s\nwant\n%s", got, want)
			}
			if label, ok := session.GetLabel(target); ok {
				t.Errorf("a reader of the labels gets %q, want the label cleared", label)
			}
		})
	}
}

// setupNullMemberRows are the rows of the pinned Pi package's record (coding/testdata/entry-null-members-pi.jsonl, which
// TestSessionEntryNullMembersMatchPi holds to the package) for the four appends every SDK's setup_null_members command makes, in call order, and
// what a reader of the reloaded file gets for the optional member.
var setupNullMemberRows = []struct{ row, read string }{
	{"api custom data omitted", "data=undefined"},
	{"api custom data null", "data=null"},
	{"api custom_message details omitted", "details=undefined"},
	{"api custom_message details null", "details=null"},
}

// wantSetupNullMemberLines are the lines Pi wrote for setupNullMemberRows, each with its reader's value.
func wantSetupNullMemberLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "coding", "testdata", "entry-null-members-pi.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	written := map[string][]string{}
	for record := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		var row struct {
			Row   string   `json:"row"`
			Error string   `json:"error"`
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal([]byte(record), &row); err != nil {
			t.Fatal(err)
		}
		if row.Error == "" {
			written[row.Row] = row.Lines
		}
	}
	want := make([]string, 0, len(setupNullMemberRows))
	for _, row := range setupNullMemberRows {
		lines := written[row.row]
		if len(lines) != 1 {
			t.Fatalf("the Pi record has %d lines for the row %q, want 1", len(lines), row.row)
		}
		want = append(want, lines[0]+" => "+row.read)
	}
	return want
}

// Pi: packages/coding-agent/src/core/agent-session-runtime.ts:254-257 hands newSession's setup callback the replacement Session's SessionManager, whose
// appendCustomEntry and appendCustomMessageEntry (session-manager.ts:1296-1312, 1350-1366) store each member as given: JSON.stringify writes a null and
// leaves out an undefined one. An SDK's setup callback says the two in its own language: Node undefined and null, Go nil and json.RawMessage("null"),
// Python None and pig_sdk.NULL, Rust Value::Null and the _opt siblings' Some(Value::Null), Lisp NIL and :NULL. The row binds each append to the line the
// pinned Pi package wrote for it and to the reloaded value, so an SDK that drops the null or writes one for an omitted member fails, isolated, packed
// and fused.
func TestSetupSessionNullMembersAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	want := strings.Join(wantSetupNullMemberLines(t), "\n")
	for _, test := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("setup null members complete")
				}
			})
			dir := t.TempDir()
			session, err := icodingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range []string{
				`{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.001Z","message":{"role":"user","content":"u","timestamp":1}}`,
				`{"type":"message","id":"00000002","parentId":"00000001","timestamp":"2026-01-01T00:00:00.002Z","message":{"role":"assistant","content":[{"type":"text","text":"a"}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`,
			} {
				if err := session.AppendEntry(json.RawMessage(line)); err != nil {
					t.Fatal(err)
				}
			}
			if test.name != "inproc-go" {
				if h.bridge == nil {
					t.Fatal("the harness has no host bridge to bind newSession on")
				}
				// The replacement Session's manager behind the setup callback, as the runtime hands it over (agent-session-runtime.ts:254-257).
				h.bridge.SetHostAction("newSession", func(_ context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
					if opts == nil || opts.Setup == nil {
						return extension.CancelledResult{}, errors.New("the newSession call named no setup callback")
					}
					return extension.CancelledResult{}, opts.Setup(session)
				})
				command, ok := findCommand(h.runner, "setup_null_members")
				if !ok {
					t.Fatal("setup_null_members command not registered")
				}
				if err := command.Handler(t.Context(), ""); err != nil {
					t.Fatalf("setup_null_members command: %v", err)
				}
			} else {
				// The in-process reference: the manager's own API, where a nil `any` is the omitted argument and json.RawMessage("null") is null.
				for _, call := range []func() (string, error){
					func() (string, error) { return session.AppendCustomEntry("api-custom", nil) },
					func() (string, error) { return session.AppendCustomEntry("api-custom", json.RawMessage("null")) },
					func() (string, error) { return session.AppendCustomMessageEntry("api-message", "x", false, nil) },
					func() (string, error) {
						return session.AppendCustomMessageEntry("api-message", "x", true, json.RawMessage("null"))
					},
				} {
					if _, err := call(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := strings.Join(entryNullMemberLines(t, session.Path()), "\n"); got != want {
				t.Errorf("the setup callback left the lines\n%s\nwant\n%s", got, want)
			}
		})
	}
}
