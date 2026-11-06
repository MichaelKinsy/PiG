//go:build parity

package runner

import (
	"strings"
	"testing"
)

const (
	pigOnlyRecord = `{"type":"queue_update","steering":[],"followUp":[]}`
	otherRecord   = `{"type":"queue_update","steering":[""],"followUp":[]}`
)

var pigOnlyRule = JSONPigOnlyRecordRule{Divergence: "D250", Record: pigOnlyRecord, Count: 1, BeforeType: "message_start", Reason: "Pi 1.5.0 leaves a delivered empty queued message in the pending queue."}

// outcome runs the assertions of a scenario that compares JSONL output under pigOnlyRule, as the runner does, over one run per side.
func outcome(pig, pi string, rules ...JSONPigOnlyRecordRule) *ScenarioOutcome {
	if rules == nil {
		rules = []JSONPigOnlyRecordRule{pigOnlyRule}
	}
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "pig-only", Driver: "rpc-mode", Covers: []string{"x"}, Assert: AssertSpec{JSONOutputEqual: true, JSONPigOnlyRecords: rules}},
		Pig:      SystemResults{System: "pig", Runs: []Result{{System: "pig", Output: pig}}},
		Pi:       SystemResults{System: "pi", Runs: []Result{{System: "pi", Output: pi}}},
	}
	EvaluateOutcome(o)
	return o
}

func failures(o *ScenarioOutcome) string { return strings.Join(o.Failures, "\n") }

func TestPigOnlyRecordIsDroppedFromPigOnly(t *testing.T) {
	pi := "{\"type\":\"agent_start\"}\n{\"type\":\"message_start\",\"n\":1}\n"
	pig := "{\"type\":\"agent_start\"}\n" + pigOnlyRecord + "\n{\"type\":\"message_start\",\"n\":1}\n"
	if o := outcome(pig, pi); !o.Passed() {
		t.Fatalf("the declared divergence failed: %s", failures(o))
	}
	if o := outcome(pig, pi, JSONPigOnlyRecordRule{}); o.Passed() {
		t.Fatal("an unusable rule passed")
	}
	// Without the rule the extra record is a difference.
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "pig-only", Driver: "rpc-mode", Covers: []string{"x"}, Assert: AssertSpec{JSONOutputEqual: true}},
		Pig:      SystemResults{System: "pig", Runs: []Result{{Output: pig}}},
		Pi:       SystemResults{System: "pi", Runs: []Result{{Output: pi}}},
	}
	if EvaluateOutcome(o); o.Passed() {
		t.Fatal("the extra record compared equal without the rule")
	}
	// Pi's output is never edited: the same record in Pi's output stays a difference.
	piToo := "{\"type\":\"agent_start\"}\n" + pigOnlyRecord + "\n{\"type\":\"message_start\",\"n\":1}\n"
	if o := outcome(pig, piToo); o.Passed() {
		t.Fatal("a record Pi emitted was dropped from Pi's output")
	}
	// A key order that differs still names the same record; any other value is another record.
	reordered := "{\"type\":\"agent_start\"}\n{\"followUp\":[],\"steering\":[],\"type\":\"queue_update\"}\n{\"type\":\"message_start\",\"n\":1}\n"
	if o := outcome(reordered, pi); !o.Passed() {
		t.Fatalf("an equal record in another key order was kept: %s", failures(o))
	}
	different := "{\"type\":\"agent_start\"}\n" + otherRecord + "\n{\"type\":\"message_start\",\"n\":1}\n"
	if o := outcome(different, pi); o.Passed() || !strings.Contains(failures(o), "D250") {
		t.Fatalf("a different queue_update was dropped: %s", failures(o))
	}
}

func TestPigOnlyRecordRequiresTheExactCount(t *testing.T) {
	pi := "{\"type\":\"agent_start\"}\n{\"type\":\"message_start\"}\n"
	for name, pig := range map[string]string{
		"divergence gone":  pi,
		"extra occurrence": "{\"type\":\"agent_start\"}\n" + pigOnlyRecord + "\n" + pigOnlyRecord + "\n{\"type\":\"message_start\"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if o := outcome(pig, pi); o.Passed() || !strings.Contains(failures(o), "D250") {
				t.Fatalf("count mismatch accepted: %s", failures(o))
			}
		})
	}
	// Each rule counts its own records; the second rule here finds none.
	second := pigOnlyRule
	second.Record = otherRecord
	pig := "{\"type\":\"agent_start\"}\n" + pigOnlyRecord + "\n{\"type\":\"message_start\"}\n"
	if o := outcome(pig, pi, pigOnlyRule, second); o.Passed() {
		t.Fatal("a rule that found no record passed")
	}
	// Two rules drop their own records.
	both := "{\"type\":\"agent_start\"}\n" + otherRecord + "\n{\"type\":\"message_start\"}\n" + pigOnlyRecord + "\n{\"type\":\"message_start\"}\n"
	piBoth := "{\"type\":\"agent_start\"}\n{\"type\":\"message_start\"}\n{\"type\":\"message_start\"}\n"
	if o := outcome(both, piBoth, pigOnlyRule, second); !o.Passed() {
		t.Fatalf("two declared records failed: %s", failures(o))
	}
}

func TestPigOnlyRecordPinsItsPosition(t *testing.T) {
	pi := "{\"type\":\"agent_start\"}\n{\"type\":\"message_start\"}\n{\"type\":\"message_end\"}\n"
	for name, pig := range map[string]string{
		"moved after the message":     "{\"type\":\"agent_start\"}\n{\"type\":\"message_start\"}\n" + pigOnlyRecord + "\n{\"type\":\"message_end\"}\n",
		"moved before agent_start":    pigOnlyRecord + "\n{\"type\":\"agent_start\"}\n{\"type\":\"message_start\"}\n{\"type\":\"message_end\"}\n",
		"last record":                 "{\"type\":\"agent_start\"}\n{\"type\":\"message_start\"}\n{\"type\":\"message_end\"}\n" + pigOnlyRecord + "\n",
		"followed by an untyped line": "{\"type\":\"agent_start\"}\n" + pigOnlyRecord + "\n{}\n{\"type\":\"message_start\"}\n{\"type\":\"message_end\"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if o := outcome(pig, pi); o.Passed() || !strings.Contains(failures(o), "D250") {
				t.Fatalf("a moved record passed: %s", failures(o))
			}
		})
	}
	moved := "{\"type\":\"agent_start\"}\n" + pigOnlyRecord + "\n{\"type\":\"message_start\"}\n{\"type\":\"message_end\"}\n"
	if o := outcome(moved, pi); !o.Passed() {
		t.Fatalf("the declared position failed: %s", failures(o))
	}
}

func TestPigOnlyRecordRulesAreValidated(t *testing.T) {
	for name, rule := range map[string]JSONPigOnlyRecordRule{
		"divergence": {Divergence: "250", Record: pigOnlyRecord, Count: 1, BeforeType: "message_start", Reason: "r"},
		"reason":     {Divergence: "D250", Record: pigOnlyRecord, Count: 1, BeforeType: "message_start"},
		"count":      {Divergence: "D250", Record: pigOnlyRecord, BeforeType: "message_start", Reason: "r"},
		"before":     {Divergence: "D250", Record: pigOnlyRecord, Count: 1, Reason: "r"},
		"record":     {Divergence: "D250", Record: `[]`, Count: 1, BeforeType: "message_start", Reason: "r"},
		"several":    {Divergence: "D250", Record: pigOnlyRecord + "\n" + pigOnlyRecord, Count: 1, BeforeType: "message_start", Reason: "r"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := dropPigOnlyRecords(Result{Output: pigOnlyRecord + "\n{\"type\":\"message_start\"}\n"}, []JSONPigOnlyRecordRule{rule}); err == nil {
				t.Fatal("dropPigOnlyRecords accepted an invalid rule")
			}
			sc := &Scenario{Name: "pig-only", Driver: "rpc-mode", Covers: []string{"x"}, Assert: AssertSpec{JSONOutputEqual: true, JSONPigOnlyRecords: []JSONPigOnlyRecordRule{rule}}}
			if err := validate(sc); err == nil {
				t.Fatal("validate accepted an invalid rule")
			}
		})
	}
	sc := &Scenario{Name: "pig-only", Driver: "rpc-mode", Covers: []string{"x"}, Assert: AssertSpec{JSONPigOnlyRecords: []JSONPigOnlyRecordRule{pigOnlyRule}}}
	if err := validate(sc); err == nil || !strings.Contains(err.Error(), "json_output_equal") {
		t.Fatalf("json_pig_only_records accepted without json_output_equal: %v", err)
	}
	sc.Assert.JSONOutputEqual = true
	if err := validate(sc); err != nil {
		t.Fatal(err)
	}
}
