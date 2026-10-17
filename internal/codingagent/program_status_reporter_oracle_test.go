package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type programStatusOp struct {
	Op     string         `json:"op"`
	Event  map[string]any `json:"event,omitempty"`
	Source string         `json:"source,omitempty"`
	Status map[string]any `json:"status,omitempty"`
	Value  string         `json:"value,omitempty"`
}

type programStatusReport struct {
	State   string `json:"state"`
	Kind    string `json:"kind,omitempty"`
	Message string `json:"message,omitempty"`
}

// goEvent builds the Session event of program-status-reporter.ts handleEvent's input; ok is false for an event the reporter ignores.
func (op programStatusOp) goEvent() agent.AgentEvent {
	ev := op.Event
	str := func(key string) string { s, _ := ev[key].(string); return s }
	flag := func(key string) bool { b, _ := ev[key].(bool); return b }
	switch ev["type"] {
	case "agent_start":
		return agent.AgentStartEvent{}
	case "message_end":
		message, _ := ev["message"].(map[string]any)
		if message["role"] != "assistant" {
			return agent.MessageEndEvent{Message: agent.AgentMessage{}}
		}
		reason, _ := message["stopReason"].(string)
		errorMessage, _ := message["errorMessage"].(string)
		return agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{StopReason: ai.StopReason(reason), ErrorMessage: errorMessage}}}
	case "compaction_start":
		return agent.CompactionStartEvent{Reason: str("reason")}
	case "compaction_end":
		return agent.CompactionEndEvent{Reason: str("reason"), Aborted: flag("aborted"), ErrorMessage: str("errorMessage")}
	case "agent_settled":
		return agent.AgentSettledEvent{Aborted: flag("aborted")}
	case "session_info_changed":
		return agent.SessionInfoChangedEvent{}
	case "turn_start":
		return agent.TurnStartEvent{}
	}
	return nil
}

// program-status-reporter.ts against pinned Pi: random sequences of session events, dialogs, resets, explicit reports and session renames produce
// the same OSC 7501 reports in the same order, including which repeated status is suppressed. Messages are the session name, dialog titles and the first line of errors.
func TestProgramStatusReporterRandomSequencesMatchPi(t *testing.T) {
	sequences := programStatusCorpus()
	input, err := json.Marshal(sequences)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/program_status_reporter.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]programStatusReport
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, ops := range sequences {
		got := runProgramStatusSequence(ops)
		if !reflect.DeepEqual(got, expected[i]) && (len(got) != 0 || len(expected[i]) != 0) {
			if failures++; failures <= 3 {
				ops, _ := json.Marshal(ops)
				t.Errorf("sequence %d differs from Pi\n ops %s\n Pig %+v\n Pi  %+v", i, ops, got, expected[i])
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d sequences differ from Pi", failures, len(sequences))
	}
}

// TestProgramStatusReporterProbeDump prints the corpus for the Pi side of the interactive-rendering/56 parity scenario.
func TestProgramStatusReporterProbeDump(t *testing.T) {
	line, err := json.Marshal(programStatusCorpus())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("program-status-probes:%s\n", line)
}

// TestProgramStatusReporterParity prints PiG's reports, one JSON line per sequence, for the interactive-rendering/56 parity scenario.
func TestProgramStatusReporterParity(t *testing.T) {
	for _, ops := range programStatusCorpus() {
		reports := runProgramStatusSequence(ops)
		if reports == nil {
			reports = []programStatusReport{}
		}
		line, err := json.Marshal(reports)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("program-status-observation:%s\n", line)
	}
}

// programStatusCorpus is 400 seeded sequences of session events, dialogs, resets, explicit reports and session renames.
func programStatusCorpus() [][]programStatusOp {
	rng := rand.New(rand.NewSource(20261011))
	errorMessages := []string{"", "overloaded", "Invalid API key\n{details}", "  padded  ", "\r\nleading blank", "line\r\nmore", "\u00a0nbsp\u00a0", "\ufeffbom\ufeff", "日本語\nx", "   ", "\n", "\r", "tab\there", "a\u2028b", "x\r\ny\nz"}
	names := []string{"", "Fix login", " padded name ", "日本語", "a\x07b", "n"}
	reasons := []string{"manual", "threshold", "overflow"}
	sources := []string{"a", "b", "extension-dialog"}
	kinds := []string{"permission", "question", "auth"}
	pick := func(from []string) string { return from[rng.Intn(len(from))] }
	maybe := func(p float64) bool { return rng.Float64() < p }
	var sequences [][]programStatusOp
	for range 400 {
		var ops []programStatusOp
		for range 1 + rng.Intn(24) {
			switch rng.Intn(14) {
			case 0:
				ops = append(ops, programStatusOp{Op: "event", Event: map[string]any{"type": "agent_start"}})
			case 1, 2:
				message := map[string]any{"role": pick([]string{"assistant", "assistant", "user", "toolResult"})}
				if maybe(0.5) {
					message["stopReason"] = pick([]string{"stop", "error", "aborted", "length", "toolUse"})
				}
				if maybe(0.6) {
					message["errorMessage"] = pick(errorMessages)
				}
				ops = append(ops, programStatusOp{Op: "event", Event: map[string]any{"type": "message_end", "message": message}})
			case 3:
				ops = append(ops, programStatusOp{Op: "event", Event: map[string]any{"type": "compaction_start", "reason": pick(reasons)}})
			case 4, 5:
				event := map[string]any{"type": "compaction_end", "reason": pick(reasons), "aborted": maybe(0.3)}
				if maybe(0.6) {
					event["errorMessage"] = pick(errorMessages)
				}
				ops = append(ops, programStatusOp{Op: "event", Event: event})
			case 6, 7:
				ops = append(ops, programStatusOp{Op: "event", Event: map[string]any{"type": "agent_settled", "aborted": maybe(0.3)}})
			case 8:
				ops = append(ops, programStatusOp{Op: "event", Event: map[string]any{"type": pick([]string{"session_info_changed", "turn_start"})}})
			case 9, 10:
				op := programStatusOp{Op: "blocked", Source: pick(sources)}
				if maybe(0.65) {
					op.Status = map[string]any{"kind": pick(kinds), "message": pick(names[1:])}
				}
				ops = append(ops, op)
			case 11:
				ops = append(ops, programStatusOp{Op: pick([]string{"reset", "report"})})
			default:
				ops = append(ops, programStatusOp{Op: "name", Value: pick(names)})
			}
		}
		sequences = append(sequences, ops)
	}
	return sequences
}

// runProgramStatusSequence drives PiG's reporter through ops and returns what it reported to its terminal.
func runProgramStatusSequence(ops []programStatusOp) []programStatusReport {
	var got []programStatusReport
	terminal := &programStatusTerminal{}
	name := ""
	reporter := NewProgramStatusReporter(func() tui.Terminal { return terminal }, func() string { return name })
	for _, op := range ops {
		switch op.Op {
		case "event":
			if event := op.goEvent(); event != nil {
				reporter.HandleEvent(event)
			}
		case "blocked":
			if op.Status == nil {
				reporter.SetBlocked(op.Source, nil)
			} else {
				reporter.SetBlocked(op.Source, &BlockedStatus{Kind: tui.ProgramStatusKind(op.Status["kind"].(string)), Message: op.Status["message"].(string)})
			}
		case "reset":
			reporter.Reset()
		case "report":
			reporter.Report()
		case "name":
			name = op.Value
		}
	}
	for _, report := range terminal.reports {
		got = append(got, programStatusReport{State: string(report.State), Kind: string(report.Kind), Message: report.Message})
	}
	return got
}
