package coding

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// nullMembersRecord is what one row leaves behind: the lines its action added to the session file, with identities and times replaced, and what a reader of the
// reloaded file gets for each optional member of those entries. test/parity/testdata/session-null-members-pi.mjs writes the same record from the pinned Pi
// package, member for member.
type nullMembersRecord struct {
	Row   string   `json:"row"`
	Error string   `json:"error"`
	Lines []string `json:"lines"`
	Read  []string `json:"read"`
	// Label is what a reader of the labels gets for the labelled entry after a row that sets a label: the label, or "undefined" for a cleared one.
	Label string `json:"label,omitempty"`
}

// nullMembersGolden reads the records the pinned Pi package wrote, in order (testdata/entry-null-members-pi.jsonl).
func nullMembersGolden(t *testing.T) (rows []string, records map[string]string) {
	t.Helper()
	file, err := os.Open("testdata/entry-null-members-pi.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	records = map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var record struct {
			Row string `json:"row"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, record.Row)
		records[record.Row] = scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows, records
}

var nullMembersNormalize = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`"(id|parentId|targetId|firstKeptEntryId|fromId)":"[0-9a-f]{8}"`), `"${1}":"#"`},
	{regexp.MustCompile(`"timestamp":"\d{4}-[^"]*Z"`), `"timestamp":"T"`},
	{regexp.MustCompile(`"timestamp":\d+`), `"timestamp":0`},
	{regexp.MustCompile(`"tokensBefore":\d+`), `"tokensBefore":0`},
	{regexp.MustCompile(`"systemMessage":\{.*\}\}$`), `"systemMessage":{}}`},
}

// nullMembersNames are the members a reader is asked for, per entry type.
var nullMembersNames = map[string][]string{
	"custom": {"data"}, "custom_message": {"content", "display", "details"}, "compaction": {"details", "usage", "fromHook"},
	"branch_summary": {"details", "usage", "fromHook"}, "label": {"label"}, "session_info": {"name"}, "context_edit": {"replacement"}, "usage": {"note"},
	"model_change": {"provider", "modelId"}, "thinking_level_change": {"thinkingLevel"},
}

// nullMembersRead is what a reader of the entry gets for each optional member: undefined for a member the record lacks, null, or the JavaScript type of its value.
func nullMembersRead(raw json.RawMessage) string {
	if raw == nil {
		return "missing"
	}
	var members map[string]json.RawMessage
	var kind string
	if json.Unmarshal(raw, &members) != nil || json.Unmarshal(members["type"], &kind) != nil {
		return "undecodable"
	}
	states := make([]string, 0, 3)
	for _, name := range nullMembersNames[kind] {
		state := "undefined"
		if value, present := members[name]; present {
			switch value[0] {
			case 'n':
				state = "null"
			case '{', '[':
				state = "object"
			case '"':
				state = "string"
			case 't', 'f':
				state = "boolean"
			default:
				state = "number"
			}
		}
		states = append(states, name+"="+state)
	}
	return kind + ":" + strings.Join(states, ",")
}

// Pi stores every member of a session entry as its caller gave it and JSON.stringify decides what the file holds: a member whose value is null is written as
// null and read back as null, one that is undefined is left out (session-manager.ts appendCustomEntry, appendCustomMessageEntry, appendCompaction,
// branchWithSummary, appendLabelChange; agent-session.ts _applyBoundaryDrafts, compact, navigateTree). Each row appends through one path with the member left
// out, null or a value; its record equals the one the pinned Pi package writes, and the parity scenario session/39-entry-null-members compares the two live.
func TestSessionEntryNullMembersMatchPi(t *testing.T) {
	order, golden := nullMembersGolden(t)
	const usage = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	usageValue := ai.Usage{Input: 1, Output: 2, TotalTokens: 3}
	var h *recoveryHarness
	// What the handlers return next, as an SDK sends it: the drafts of agent_before_settle, and the members after the required ones of a
	// session_before_compact result's compaction and of a session_before_tree result's summary.
	var drafts string
	var compactionMembers, summaryMembers *string
	handlers := map[string][]extension.HandlerFn{
		"agent_before_settle": {func(...any) (any, error) {
			if drafts == "" {
				return nil, nil
			}
			result := json.RawMessage(`{"entries":` + drafts + `}`)
			drafts = ""
			return result, nil
		}},
		"session_before_compact": {func(args ...any) (any, error) {
			if compactionMembers == nil {
				return nil, nil
			}
			raw, err := json.Marshal(args[0].(extension.SessionBeforeCompactEvent).Preparation)
			if err != nil {
				return nil, err
			}
			var preparation struct {
				FirstKeptEntryID string
				TokensBefore     int
			}
			if err := json.Unmarshal(raw, &preparation); err != nil {
				return nil, err
			}
			return json.RawMessage(fmt.Sprintf(`{"compaction":{"summary":"hook-compaction","firstKeptEntryId":%q,"tokensBefore":%d%s}}`, preparation.FirstKeptEntryID, preparation.TokensBefore, *compactionMembers)), nil
		}},
		"session_before_tree": {func(...any) (any, error) {
			if summaryMembers == nil {
				return nil, nil
			}
			return json.RawMessage(`{"summary":{"summary":"hook-branch"` + *summaryMembers + `}}`), nil
		}},
	}
	responses := make([]scriptedResponse, 60)
	for i := range responses {
		responses[i] = boundaryReply("ok", ai.StopReasonStop, 0)
	}
	h = newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":false,"keepRecentTokens":1,"reserveTokens":0}}`, extension: extension.Extension{Handlers: handlers}}, responses...)
	var extensionErrors []string
	h.session.ExtensionRunner().AddErrorListener(func(err *extension.ExtensionError) {
		extensionErrors = append(extensionErrors, err.Event+": "+err.Error)
	})
	prompt := func() { boundaryPrompt(t, h, "PROMPT "+strings.Repeat("x", 2000)) }
	prompt()
	path := h.session.Path()
	if path == "" {
		t.Fatal("the session has no file")
	}
	fileLines := func() []string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	type pendingRow struct {
		name     string
		from, to int
		err      error
		label    string
	}
	var rows []pendingRow
	var labelSeen string
	row := func(name string, action func() error) {
		t.Helper()
		from := len(fileLines())
		labelSeen = ""
		err := action()
		rows = append(rows, pendingRow{name, from, len(fileLines()), err, labelSeen})
	}
	in := func() *icodingagent.Session { return h.session.Inner() }
	appendID := func(_ string, err error) error { return err }
	null := json.RawMessage("null")
	value := json.RawMessage(`{"k":1}`)
	send := func(payload string, trigger bool) func() error {
		return func() error {
			// The message as the host decodes it from an extension's sendMessage call.
			var message extension.CustomMessageRef
			if err := json.Unmarshal([]byte(payload), &message); err != nil {
				return err
			}
			var options *extension.SendMessageOptions
			if trigger {
				options = &extension.SendMessageOptions{TriggerTurn: &trigger}
			}
			return h.session.SendCustomMessage(t.Context(), message, options)
		}
	}
	draft := func(name, entries string) {
		t.Helper()
		row(name, func() error {
			drafts = entries
			prompt()
			return nil
		})
	}

	// An extension: pi.appendEntry, pi.sendMessage, pi.setSessionName, boundary drafts, and the results of session_before_compact and session_before_tree.
	row("extension appendEntry data omitted", func() error { return appendID(h.session.AppendCustomEntry("extension-custom", nil)) })
	row("extension appendEntry data null", func() error { return appendID(h.session.AppendCustomEntry("extension-custom", null)) })
	row("extension appendEntry data value", func() error { return appendID(h.session.AppendCustomEntry("extension-custom", value)) })
	row("extension sendMessage details omitted", send(`{"customType":"extension-message","content":"x","display":false}`, false))
	row("extension sendMessage details null", send(`{"customType":"extension-message","content":"x","display":true,"details":null}`, false))
	row("extension sendMessage details value", send(`{"customType":"extension-message","content":"x","display":true,"details":{"k":1}}`, false))
	row("extension sendMessage details null, triggerTurn", send(`{"customType":"extension-message","content":"x","display":true,"details":null}`, true))
	row("extension sendMessage details omitted, triggerTurn", send(`{"customType":"extension-message","content":"x","display":true}`, true))
	row("extension setSessionName value", func() error { return h.session.SetSessionName("n") })
	draft("draft custom data omitted", `[{"type":"custom","customType":"draft-custom"}]`)
	draft("draft custom data null", `[{"type":"custom","customType":"draft-custom","data":null}]`)
	draft("draft custom data value", `[{"type":"custom","customType":"draft-custom","data":{"k":1}}]`)
	draft("draft custom_message details omitted", `[{"type":"custom_message","customType":"draft-message","content":"x","display":false}]`)
	draft("draft custom_message details null", `[{"type":"custom_message","customType":"draft-message","content":"x","display":false,"details":null}]`)
	draft("draft custom_message details value", `[{"type":"custom_message","customType":"draft-message","content":"x","display":true,"details":{"k":1}}]`)
	draft("draft compaction members omitted", `[{"type":"compaction","summary":"draft-compaction"}]`)
	draft("draft compaction members null", `[{"type":"compaction","summary":"draft-compaction","firstKeptEntryId":null,"details":null,"usage":null}]`)
	draft("draft compaction members value", `[{"type":"compaction","summary":"draft-compaction","firstKeptEntryId":null,"details":{"k":1},"usage":`+usage+`}]`)
	hookMembers := []struct{ name, members string }{{"omitted", ""}, {"null", `,"details":null,"usage":null`}, {"value", `,"details":{"k":1},"usage":` + usage}}
	for _, tc := range hookMembers {
		row("session_before_compact result members "+tc.name, func() error {
			prompt()
			compactionMembers = &tc.members
			defer func() { compactionMembers = nil }()
			_, err := h.session.Compact(context.Background(), "")
			return err
		})
	}
	for _, tc := range hookMembers {
		row("session_before_tree summary members "+tc.name, func() error {
			prompt()
			summaryMembers = &tc.members
			defer func() { summaryMembers = nil }()
			_, err := h.session.NavigateTree(context.Background(), boundaryLastUser(t, h), NavigateTreeOptions{Summarize: true})
			return err
		})
	}

	// The session API: the Session's own append methods, as a program that holds the session calls them.
	userID := boundaryLastUser(t, h)
	leaf := func() *string { return in().GetLeafID() }
	empty, label := "", "x"
	row("api custom data omitted", func() error { return appendID(in().AppendCustomEntry("api-custom", nil)) })
	row("api custom data null", func() error { return appendID(in().AppendCustomEntry("api-custom", null)) })
	row("api custom data value", func() error { return appendID(in().AppendCustomEntry("api-custom", value)) })
	row("api custom_message details omitted", func() error { return appendID(in().AppendCustomMessageEntry("api-message", "x", false, nil)) })
	row("api custom_message details null", func() error { return appendID(in().AppendCustomMessageEntry("api-message", "x", true, null)) })
	row("api custom_message details value", func() error { return appendID(in().AppendCustomMessageEntry("api-message", "x", true, value)) })
	row("api compaction members omitted", func() error {
		return appendID(in().AppendCompactionHookUndefined("api-compaction", userID, 10, nil, nil))
	})
	row("api compaction details and usage null", func() error { return nullMembersCompaction(in(), userID) })
	row("api compaction members value", func() error {
		return appendID(in().AppendCompaction("api-compaction", userID, 10, value, true, &usageValue))
	})
	row("api compaction fromHook false", func() error { return appendID(in().AppendCompaction("api-compaction", userID, 10, nil, false, nil)) })
	row("api compaction firstKeptEntryId null", func() error { return appendID(in().AppendCompactionHookUndefined("api-compaction", "", 10, nil, nil)) })
	row("api branch_summary details and usage omitted", func() error { return appendID(in().BranchWithSummary(leaf(), "api-branch", nil, false, nil)) })
	row("api branch_summary details and usage null", func() error { return nullMembersBranchSummary(in(), leaf()) })
	row("api branch_summary members value", func() error { return appendID(in().BranchWithSummary(leaf(), "api-branch", value, true, &usageValue)) })
	row("api label omitted", func() error { return appendID(in().AppendLabelChange(userID, nil)) })
	row("api label null", func() error { return nullMembersLabel(in(), userID) })
	row("api label empty", func() error { return appendID(in().AppendLabelChange(userID, &empty)) })
	row("api label value", func() error { return appendID(in().AppendLabelChange(userID, &label)) })
	row("api usage note omitted", func() error { _, err := in().AppendUsage("k", "p", "m", usageValue, ""); return err })
	row("api usage note empty", func() error { _, err := in().AppendUsage("k", "p", "m", usageValue, ""); return err })
	row("api usage note value", func() error { _, err := in().AppendUsage("k", "p", "m", usageValue, "n"); return err })
	row("api session_info name empty", func() error { return appendID(in().AppendSessionInfo("")) })
	row("api session_info name value", func() error { return appendID(in().AppendSessionInfo(" n\nm ")) })
	row("api branch_summary fromHook omitted", func() error {
		return appendID(in().BranchWithSummaryEntry(leaf(), icodingagent.BranchSummaryEntry{Summary: "api-branch", FromHookAbsent: true}))
	})

	// A custom message whose writer left display or content out: Pi stores undefined and the entry has no such member.
	draft("draft custom_message display omitted", `[{"type":"custom_message","customType":"draft-message","content":"x"}]`)
	draft("draft custom_message content omitted", `[{"type":"custom_message","customType":"draft-message","display":true}]`)
	row("extension sendMessage display omitted", send(`{"customType":"extension-message","content":"x"}`, false))
	row("extension sendMessage display omitted, triggerTurn", send(`{"customType":"extension-message","content":"x"}`, true))

	// pi.setLabel as the host takes it from an extension: without a label, with a null one, an empty one and a value, each after a label a reader can
	// see cleared.
	bridge := subprocess.NewUIBridge(nil)
	BindSessionReadActions(bridge, func() *Session { return h.session }, h.session.CWD(), t.TempDir())
	setLabel := func(name, member string) {
		row("extension setLabel "+name, func() error {
			id := boundaryLastUser(t, h)
			for _, label := range []string{`,"label":"mark"`, member} {
				result, err := bridge.HandleCall("null-members", &subprocess.CallPayload{Method: "setLabel", Args: json.RawMessage(fmt.Sprintf(`{"entryId":%q%s}`, id, label))})
				if err != nil {
					return err
				}
				if result != nil && result.Error != nil {
					return fmt.Errorf("setLabel: %+v", result.Error)
				}
			}
			labelSeen = "undefined"
			if label, ok := in().GetLabel(id); ok {
				labelSeen = label
			}
			return nil
		})
	}
	setLabel("omitted", "")
	setLabel("null", `,"label":null`)
	setLabel("empty", `,"label":""`)
	setLabel("value", `,"label":"x"`)

	// A usage an extension wrote in another member order and with members ai.Usage has no field for: Pi stores the author's object.
	const usageReordered = `{"cost":{"total":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"totalTokens":3,"output":2,"input":1,"cacheRead":0,"cacheWrite":0}`
	usageAuthored := `,"usage":` + strings.TrimSuffix(usageReordered, "}") + `,"extra":1}`
	draft("draft compaction usage in another member order", `[{"type":"compaction","summary":"draft-compaction","usage":`+usageReordered+`}]`)
	draft("draft compaction usage with extra members", `[{"type":"compaction","summary":"draft-compaction","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0,"extra":2},"extra":1}}]`)
	row("session_before_compact result usage as its author wrote it", func() error {
		prompt()
		compactionMembers = &usageAuthored
		defer func() { compactionMembers = nil }()
		_, err := h.session.Compact(context.Background(), "")
		return err
	})
	row("session_before_tree summary usage as its author wrote it", func() error {
		prompt()
		summaryMembers = &usageAuthored
		defer func() { summaryMembers = nil }()
		_, err := h.session.NavigateTree(context.Background(), boundaryLastUser(t, h), NavigateTreeOptions{Summarize: true})
		return err
	})

	// A display written as null, which the declaration does not allow and nothing checks: Pi stores the null.
	draft("draft custom_message display null", `[{"type":"custom_message","customType":"draft-message","content":"x","display":null}]`)
	row("extension sendMessage display null", send(`{"customType":"extension-message","content":"x","display":null}`, false))
	// A message sent without content or with a null one, which the declaration does not allow: sendCustomMessage reads `message.content ?? []`, so both
	// write an empty list.
	row("extension sendMessage content omitted", send(`{"customType":"extension-message","display":true}`, false))
	row("extension sendMessage content null", send(`{"customType":"extension-message","content":null,"display":true}`, false))

	entries, err := icodingagent.LoadEntriesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := map[string]json.RawMessage{}
	for _, entry := range entries {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(entry.Raw(), &identity) == nil {
			reloaded[identity.ID] = entry.Raw()
		}
	}
	lines := fileLines()
	seen := map[string]bool{}
	for _, pending := range rows {
		record := nullMembersRecord{Row: pending.name, Lines: []string{}, Read: []string{}, Label: pending.label}
		if pending.err != nil {
			record.Error = pending.err.Error()
		}
		for _, line := range lines[pending.from:pending.to] {
			var entry struct {
				Type    string `json:"type"`
				ID      string `json:"id"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Type == "message" && (entry.Message.Role == "user" || entry.Message.Role == "assistant") {
				continue
			}
			for _, rule := range nullMembersNormalize {
				line = rule.pattern.ReplaceAllString(line, rule.replacement)
			}
			record.Lines = append(record.Lines, line)
			record.Read = append(record.Read, nullMembersRead(reloaded[entry.ID]))
		}
		got := boundaryJSON(t, record)
		if os.Getenv("PIG_BOUNDARY_PROBE") == "1" {
			fmt.Println("NULL_MEMBERS " + got)
		}
		seen[pending.name] = true
		if want, ok := golden[pending.name]; !ok {
			t.Errorf("%s: testdata/entry-null-members-pi.jsonl has no record of this row", pending.name)
		} else if got != want {
			t.Errorf("%s: record differs from the pinned Pi package's\n pig: %s\n  pi: %s", pending.name, got, want)
		}
	}
	for _, name := range order {
		if !seen[name] {
			t.Errorf("%s: the pinned Pi package's row has no counterpart here", name)
		}
	}
	if len(extensionErrors) > 0 {
		t.Errorf("extension errors: %s", strings.Join(extensionErrors, "; "))
	}
}

// The rows whose null member is a typed one: the Session API takes the members as the entry type holds them.
func nullMembersCompaction(session *icodingagent.Session, firstKept string) error {
	_, err := session.AppendCompactionEntry(icodingagent.CompactionEntry{Summary: "api-compaction", FirstKeptEntryID: firstKept, TokensBefore: 10, Details: json.RawMessage("null"), UsageNull: true, FromHookAbsent: true})
	return err
}

func nullMembersBranchSummary(session *icodingagent.Session, parent *string) error {
	_, err := session.BranchWithSummaryEntry(parent, icodingagent.BranchSummaryEntry{Summary: "api-branch", Details: json.RawMessage("null"), UsageNull: true})
	return err
}

func nullMembersLabel(session *icodingagent.Session, target string) error {
	_, err := session.AppendLabelEntry(icodingagent.LabelEntry{TargetID: target, LabelNull: true})
	return err
}
