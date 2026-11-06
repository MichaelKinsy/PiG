//go:build parity

package runner

import (
	"strings"
	"testing"
)

const pigOnlyRecord = `{"type":"queue_update","steering":[],"followUp":[]}`

var pigOnlyRule = JSONPigOnlyRecordRule{Divergence: "D250", Record: pigOnlyRecord, Count: 1, Reason: "Pi 1.5.0 leaves a delivered empty queued message in the pending queue."}

func TestPigOnlyRecordsAreDroppedFromPigOnly(t *testing.T) {
	pi := Result{Output: "{\"n\":1}\n{\"n\":2}\n"}
	pig := Result{Output: "{\"n\":1}\n" + pigOnlyRecord + "\n{\"n\":2}\n"}
	if compareJSONResults(pig, pi, nil) == nil {
		t.Fatal("the extra record already compared equal")
	}
	dropped, err := dropPigOnlyRecords(pig, []JSONPigOnlyRecordRule{pigOnlyRule})
	if err != nil {
		t.Fatal(err)
	}
	if err := compareJSONResults(dropped, pi, nil); err != nil {
		t.Fatal(err)
	}
	// Pi's side is never edited: the same record in Pi's output stays a difference.
	if compareJSONResults(pi, Result{Output: pi.Output + pigOnlyRecord + "\n"}, nil) == nil {
		t.Fatal("a record Pi emitted compared equal")
	}
	// A key order or number spelling that differs still names the same record; any other value is another record.
	spelled := Result{Output: "{\"n\":1}\n{\"followUp\":[],\"steering\":[],\"type\":\"queue_update\"}\n{\"n\":2}\n"}
	if dropped, err = dropPigOnlyRecords(spelled, []JSONPigOnlyRecordRule{pigOnlyRule}); err != nil || compareJSONResults(dropped, pi, nil) != nil {
		t.Fatalf("an equal record in another key order was kept: %v", err)
	}
	other := Result{Output: "{\"n\":1}\n{\"type\":\"queue_update\",\"steering\":[\"\"],\"followUp\":[]}\n{\"n\":2}\n"}
	if _, err = dropPigOnlyRecords(other, []JSONPigOnlyRecordRule{pigOnlyRule}); err == nil {
		t.Fatal("a different queue_update was dropped")
	}
}

func TestPigOnlyRecordsRequireTheExactCount(t *testing.T) {
	for name, output := range map[string]string{
		"divergence gone":  "{\"n\":1}\n",
		"extra occurrence": "{\"n\":1}\n" + pigOnlyRecord + "\n" + pigOnlyRecord + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := dropPigOnlyRecords(Result{Output: output}, []JSONPigOnlyRecordRule{pigOnlyRule})
			if err == nil || !strings.Contains(err.Error(), "D250") {
				t.Fatalf("count mismatch accepted: %v", err)
			}
		})
	}
}

func TestPigOnlyRecordRulesAreValidated(t *testing.T) {
	for name, rule := range map[string]JSONPigOnlyRecordRule{
		"divergence": {Divergence: "250", Record: pigOnlyRecord, Count: 1, Reason: "r"},
		"reason":     {Divergence: "D250", Record: pigOnlyRecord, Count: 1},
		"count":      {Divergence: "D250", Record: pigOnlyRecord, Reason: "r"},
		"record":     {Divergence: "D250", Record: `[]`, Count: 1, Reason: "r"},
		"several":    {Divergence: "D250", Record: pigOnlyRecord + "\n" + pigOnlyRecord, Count: 1, Reason: "r"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := dropPigOnlyRecords(Result{Output: pigOnlyRecord + "\n"}, []JSONPigOnlyRecordRule{rule}); err == nil {
				t.Fatal("invalid rule accepted")
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
