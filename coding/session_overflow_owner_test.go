package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// overflowOwnerRequest describes one provider request by what tells the cases apart: the roles, and how often the prompt, the extension's summary and the
// failed attempt occur in it.
type overflowOwnerRequest struct {
	Roles   []string `json:"roles"`
	Prompt  int      `json:"prompt"`
	Summary int      `json:"summary"`
	Failed  int      `json:"failed"`
}

// overflowOwnerRecord is what one case leaves behind. test/parity/testdata/session-overflow-owner-pi.mjs writes the same record from the pinned Pi package, member
// for member.
type overflowOwnerRecord struct {
	Case            string                 `json:"case"`
	Calls           int                    `json:"calls"`
	Requests        []overflowOwnerRequest `json:"requests"`
	Entries         []string               `json:"entries"`
	TurnEnd         []string               `json:"turnEnd"`
	BeforeSettle    []string               `json:"beforeSettle"`
	BeforeCompact   []string               `json:"beforeCompact"`
	CompactionStart int                    `json:"compactionStart"`
	CompactionEnd   []string               `json:"compactionEnd"`
	Settled         int                    `json:"settled"`
	Projection      []string               `json:"projection"`
	Errors          []string               `json:"errors"`
}

// overflowOwnerGolden reads the records the pinned Pi package wrote, by case name (testdata/overflow-owner-pi.jsonl). After `make parity-deps`, this
// command writes the file again from Pi:
//
//	node --experimental-import-meta-resolve test/parity/testdata/session-overflow-owner-pi.mjs | sed 's/^OVERFLOW_OWNER_CASE //' > coding/testdata/overflow-owner-pi.jsonl
func overflowOwnerGolden(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/overflow-owner-pi.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var record struct {
			Case string `json:"case"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		golden[record.Case] = line
	}
	return golden
}

// An extension that owns compaction continues a turn after a provider overflow with the results of Pi's actionable boundaries: turn_end and agent_before_settle
// return entries (a compaction, a context edit that omits the failed attempt) and one continuation, and no method of the context is involved
// (extensions/types.ts BoundaryResult; agent-session.ts _dispatchTurnEndBoundary, _runBeforeSettleBoundary, _checkCompaction). Each case's record equals the one
// the pinned Pi package writes; the parity scenario session/38-overflow-owner-boundaries compares the two live.
func TestExtensionOwnedCompactionContinuesAfterOverflow(t *testing.T) {
	golden := overflowOwnerGolden(t)
	off, on := `{"compaction":{"enabled":false}}`, `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`
	overflow := func(messages []ai.Message) *ai.AssistantMessage {
		reply := boundaryReply("FAILED-MARK", ai.StopReasonError, 0)(messages)
		reply.ErrorMessage = "prompt is too long"
		return reply
	}
	cases := []struct {
		name, settings, mode string
		overflows            int
	}{
		{"A turn_end retain-none, before_settle continue, compaction off", off, "turnEnd+settle", 1},
		{"A2 turn_end retain-none and continue only, compaction off", off, "turnEnd", 1},
		{"B before_settle omit, kept compaction, continue, compaction off", off, "settle", 1},
		{"F before_settle continue without entries, compaction off", off, "settleOnly", 1},
		{"C turn_end retain-none, before_settle continue, compaction on", on, "turnEnd+settle", 1},
		{"D session_before_compact supplies the summary, compaction on", on, "beforeCompact", 1},
		{"G session_before_compact cancels, before_settle kept compaction, continue, compaction on", on, "cancel+settle", 1},
		{"E turn_end retain-none, before_settle continue, overflow twice, compaction off", off, "turnEnd+settle", 2},
		{"H before_settle context_edit without a replacement member, compaction off", off, "missingReplacement", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := overflowOwnerRecord{Case: tc.name, Requests: []overflowOwnerRequest{}, Entries: []string{}, TurnEnd: []string{}, BeforeSettle: []string{}, BeforeCompact: []string{}, CompactionEnd: []string{}, Projection: []string{}, Errors: []string{}}
			var h *recoveryHarness
			handled, pending := false, false
			failedID := ""
			retainNone := tc.mode == "turnEnd+settle" || tc.mode == "turnEnd"
			handlers := map[string][]extension.HandlerFn{}
			handlers["turn_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
				event := args[0].(extension.TurnEndEvent)
				record.TurnEnd = append(record.TurnEnd, fmt.Sprintf("%s:%t", event.Outcome, event.Context.CanContinue))
				if event.Outcome != extension.AgentActivityError {
					return nil, nil
				}
				failedID = event.MessageEntryID
				if !retainNone || handled {
					return nil, nil
				}
				handled, pending = true, true
				return boundaryDrafts(true, extension.SessionBoundaryDraft{Type: "compaction", Summary: "SUMMARY-MARK", FirstKeptEntryID: nil}), nil
			}}
			if tc.mode != "turnEnd" {
				handlers["agent_before_settle"] = []extension.HandlerFn{func(args ...any) (any, error) {
					event := args[0].(*extension.AgentBeforeSettleEvent)
					record.BeforeSettle = append(record.BeforeSettle, fmt.Sprintf("%s:%t", event.Outcome, event.Context.CanContinue))
					continued := true
					if tc.mode == "turnEnd+settle" {
						if !pending {
							return nil, nil
						}
						pending = false
						return extension.BoundaryResult{Continue: &continued}, nil
					}
					if handled || event.Outcome != extension.AgentActivityError {
						return nil, nil
					}
					handled = true
					switch tc.mode {
					case "settleOnly":
						return extension.BoundaryResult{Continue: &continued}, nil
					case "missingReplacement":
						// The result as an SDK sends it: a context_edit draft with no replacement member, which is not a null one.
						return json.RawMessage(fmt.Sprintf(`{"entries":[{"type":"context_edit","targetId":%q}],"continue":true}`, failedID)), nil
					}
					userID := boundaryLastUser(t, h)
					entries := []extension.SessionBoundaryDraft{}
					if tc.mode == "settle" {
						entries = append(entries, extension.SessionBoundaryDraft{Type: "context_edit", TargetID: failedID})
					}
					entries = append(entries, extension.SessionBoundaryDraft{Type: "compaction", Summary: "SUMMARY-MARK", FirstKeptEntryID: &userID})
					return boundaryDrafts(true, entries...), nil
				}}
			}
			if tc.mode == "beforeCompact" || tc.mode == "cancel+settle" {
				supply := summaryFromPreparation("SUMMARY-MARK").Handlers["session_before_compact"][0]
				handlers["session_before_compact"] = []extension.HandlerFn{func(args ...any) (any, error) {
					event := args[0].(extension.SessionBeforeCompactEvent)
					record.BeforeCompact = append(record.BeforeCompact, fmt.Sprintf("%s:%t", event.Reason, event.WillRetry))
					if tc.mode == "cancel+settle" {
						return extension.SessionBeforeCompactResult{Cancel: true}, nil
					}
					return supply(args...)
				}}
			}
			responses := []scriptedResponse{}
			for range tc.overflows {
				responses = append(responses, overflow)
			}
			responses = append(responses, boundaryReply("RECOVERED-MARK", ai.StopReasonStop, 0), boundaryReply("EXTRA-MARK", ai.StopReasonStop, 0))
			for i, response := range responses {
				responses[i] = func(messages []ai.Message) *ai.AssistantMessage {
					request := overflowOwnerRequest{Roles: []string{}}
					for _, message := range messages {
						request.Roles = append(request.Roles, message.MessageRole())
					}
					raw := boundaryJSON(t, messages)
					request.Prompt, request.Summary, request.Failed = strings.Count(raw, "PROMPT-MARK"), strings.Count(raw, "SUMMARY-MARK"), strings.Count(raw, "FAILED-MARK")
					record.Requests = append(record.Requests, request)
					// A provider's response is later than the entries appended before its request; one stamped in the same millisecond as a compaction counts as older than it (_checkCompaction).
					time.Sleep(2 * time.Millisecond)
					return response(messages)
				}
			}
			h = newBoundaryHarness(t, harnessOptions{contextWindow: 1000, maxTokens: 100, settings: tc.settings, extension: extension.Extension{Handlers: handlers}}, responses...)
			// The upstream faux provider reports a usage estimated from each request; the checks after a response read it.
			h.provider.estimateUsage = fauxUsageEstimate(t)
			h.session.ExtensionRunner().AddErrorListener(func(err *extension.ExtensionError) {
				record.Errors = append(record.Errors, err.Event+": "+err.Error)
			})
			boundaryPrompt(t, h, "PROMPT-MARK "+strings.Repeat("x", 5000))
			h.settle(t)
			record.Calls = h.provider.callCount()
			labels := map[string]string{}
			for _, entry := range h.session.Inner().GetEntries() {
				var fields map[string]any
				if err := json.Unmarshal(entry.Raw(), &fields); err != nil {
					t.Fatal(err)
				}
				label := entry.Base().Type
				switch label {
				case "message":
					message, _ := fields["message"].(map[string]any)
					label += ":" + fmt.Sprint(message["role"])
					if message["stopReason"] == "error" {
						label += ":error"
					}
				case "context_edit":
					kind := "replace"
					if fields["replacement"] == nil {
						kind = "omit"
					}
					label += ":" + kind + ":" + labels[fmt.Sprint(fields["targetId"])]
				case "compaction":
					kind := "kept:" + labels[fmt.Sprint(fields["firstKeptEntryId"])]
					if fields["firstKeptEntryId"] == fields["id"] {
						kind = "self"
					}
					label += ":" + kind
				}
				labels[fmt.Sprint(fields["id"])] = label
				record.Entries = append(record.Entries, label)
			}
			record.CompactionStart = len(boundaryEvents[agent.CompactionStartEvent](h))
			for _, end := range boundaryEvents[agent.CompactionEndEvent](h) {
				record.CompactionEnd = append(record.CompactionEnd, fmt.Sprintf("%s:aborted=%t:willRetry=%t:result=%t:%s", end.Reason, end.Aborted, end.WillRetry, end.HasResult, end.ErrorMessage))
			}
			record.Settled = len(boundaryEvents[agent.AgentSettledEvent](h))
			for _, message := range h.session.Inner().BuildSessionProjection().Messages {
				var fields struct {
					Role string `json:"role"`
				}
				if err := json.Unmarshal([]byte(boundaryJSON(t, message)), &fields); err != nil {
					t.Fatal(err)
				}
				record.Projection = append(record.Projection, fields.Role)
			}
			got := boundaryJSON(t, record)
			if os.Getenv("PIG_BOUNDARY_PROBE") == "1" {
				fmt.Println("OVERFLOW_OWNER_CASE " + got)
			}
			if want, ok := golden[tc.name]; !ok {
				t.Errorf("testdata/overflow-owner-pi.jsonl has no record of this case")
			} else if got != want {
				t.Errorf("record differs from the pinned Pi package's\n pig: %s\n  pi: %s", got, want)
			}
		})
	}
}
