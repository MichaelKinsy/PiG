//go:build parity

package runner

import (
	"strings"
	"testing"
)

// orderPairs are two JSON texts each: json_output_equal passes them (the values are equal), json_output_ordered_equal fails them (a member is written in another place).
var orderPairs = []struct {
	name, pig, pi, path string
}{
	{"top level", `{"type":"a","id":1}`, `{"id":1,"type":"a"}`, "/0"},
	{"depth three", `{"a":{"b":{"c":1,"d":2}}}`, `{"a":{"b":{"d":2,"c":1}}}`, "/0/a/b"},
	{"element of an array of objects", `{"xs":[{"p":1,"q":2},{"q":2,"p":1}]}`, `{"xs":[{"p":1,"q":2},{"p":1,"q":2}]}`, "/0/xs/1"},
	{"second record", "{\"a\":1}\n{\"x\":1,\"y\":2}", "{\"a\":1}\n{\"y\":2,\"x\":1}", "/1"},
	{"a key with a slash is one pointer segment", `{"k":{"a/b":{"x":1,"y":2},"c":1}}`, `{"k":{"a/b":{"y":2,"x":1},"c":1}}`, "/0/k/a~1b"},
	{"lone surrogate keys keep their identity", `{"\ud800":1,"\udc00":2}`, `{"\udc00":2,"\ud800":1}`, "/0"},
	{"parent before child", `{"a":{"x":1,"y":2},"b":2}`, `{"b":2,"a":{"y":2,"x":1}}`, "/0"},
}

func TestOrderedJSONComparisonSeesMemberOrder(t *testing.T) {
	for _, tc := range orderPairs {
		t.Run(tc.name, func(t *testing.T) {
			pig, pi := Result{Output: tc.pig}, Result{Output: tc.pi}
			if err := compareJSONResults(pig, pi, nil); err != nil {
				t.Fatalf("json_output_equal must pass these (equal values): %v", err)
			}
			err := compareJSONResultsOrdered(pig, pi, nil)
			if err == nil {
				t.Fatal("json_output_ordered_equal passed a pair whose member order differs")
			}
			if want := "order difference: " + tc.path + ": member order pig="; !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("want an error starting %q, got %v", want, err)
			}
			if err := compareJSONResultsOrdered(pig, pig, nil); err != nil {
				t.Fatalf("a text must equal itself: %v", err)
			}
		})
	}
}

func TestOrderedJSONComparisonNamesAValueDifferenceAsOne(t *testing.T) {
	for _, tc := range []struct{ name, pig, pi, want string }{
		{"value", `{"a":1,"b":2}`, `{"a":1,"b":3}`, "value difference: /0/b: pig=2 pi=3"},
		{"value and order", `{"a":1,"b":2}`, `{"b":2,"a":3}`, "value difference: /0/a: pig=1 pi=3"},
		{"missing member", `{"a":1,"b":2}`, `{"a":1}`, "value difference: /0/b: field presence differs"},
		{"array order", `{"a":[1,2]}`, `{"a":[2,1]}`, "value difference: /0/a/0: pig=1 pi=2"},
		{"record count", "{\"a\":1}", "{\"a\":1}\n{\"a\":1}", "value difference: : array/record count pig=1 pi=2"},
		{"duplicate key", `{"a":1,"a":2}`, `{"a":2}`, "value difference: pig: JSONL record 1: duplicate JSON key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compareJSONResultsOrdered(Result{Output: tc.pig}, Result{Output: tc.pi}, nil)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// The ordered form applies the same normalisations as json_output_equal: they act on values, and order is read from the member names alone.
func TestOrderedJSONComparisonKeepsTheValueNormalisations(t *testing.T) {
	rules := []JSONAliasRule{
		{Paths: []string{"/**/id"}, Kind: "id", Group: "g", Reason: "Independent generated identities."},
		{Paths: []string{"/**/wall_time_seconds"}, Kind: "duration", Optional: true, Reason: "A measured elapsed time Pi writes only when it ran long enough."},
	}
	for _, tc := range []struct {
		name, pig, pi string
		equal         bool
	}{
		{"number spelling", `{"n":1,"m":2.50}`, `{"n":1.0,"m":2.5}`, true},
		{"identity alias", `{"id":"a","n":1}`, `{"id":"zzz","n":1}`, true},
		{"identity alias, other order", `{"id":"a","n":1}`, `{"n":1,"id":"zzz"}`, false},
		{"optional duration absent on one side", `{"a":1,"wall_time_seconds":0.1,"b":2}`, `{"a":1,"b":2}`, true},
		{"optional duration, common members reordered", `{"b":2,"wall_time_seconds":0.1,"a":1}`, `{"a":1,"b":2}`, false},
		{"optional duration present on both sides, moved", `{"a":1,"wall_time_seconds":0.1,"b":2}`, `{"a":1,"b":2,"wall_time_seconds":0.2}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compareJSONResultsOrdered(Result{Output: tc.pig}, Result{Output: tc.pi}, rules)
			if (err == nil) != tc.equal {
				t.Fatalf("equal=%t: %v", tc.equal, err)
			}
		})
	}
	// Equal values written in the same order but spelled differently stay a value difference, as in json_output_equal.
	if err := compareJSONResultsOrdered(Result{Output: `{"s":"two  spaces"}`}, Result{Output: `{"s":"two spaces"}`}, nil); err == nil || !strings.HasPrefix(err.Error(), "value difference: /0/s") {
		t.Fatalf("string whitespace: %v", err)
	}
}

// A driver that keeps a canonical copy (rpc canonical_json sorts object keys) leaves the raw stdout beside it; both comparisons read the raw one, so the order a program wrote is what is compared.
func TestOrderedJSONComparisonReadsTheRawOutput(t *testing.T) {
	pig := Result{Output: `{"a":1,"b":2}`, RawOutput: `{"b":2,"a":1}`}
	pi := Result{Output: `{"a":1,"b":2}`, RawOutput: `{"a":1,"b":2}`}
	if err := compareJSONResults(pig, pi, nil); err != nil {
		t.Fatal(err)
	}
	if err := compareJSONResultsOrdered(pig, pi, nil); err == nil || !strings.HasPrefix(err.Error(), "order difference: /0:") {
		t.Fatalf("the canonical copy hid the raw order: %v", err)
	}
}

func TestJSONMemberOrdersListParentsBeforeChildrenInWrittenOrder(t *testing.T) {
	objects, err := jsonMemberOrders("{\"z\":{\"b\":1,\"a\":[{\"y\":0,\"x\":0}]},\"c\":2}\n{}")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, object := range objects {
		got = append(got, object.path+"="+strings.Join(object.names, ","))
	}
	if want := "/0=z,c /0/z=b,a /0/z/a/0=y,x /1="; strings.Join(got, " ") != want {
		t.Fatalf("got %q want %q", strings.Join(got, " "), want)
	}
	if _, err := jsonMemberOrders(""); err == nil {
		t.Fatal("empty output accepted")
	}
	if _, err := jsonMemberOrders(`{"a":`); err == nil {
		t.Fatal("truncated record accepted")
	}
}

// orderedOutcomeOf runs the assertions of one scenario over one run per side, as the runner does.
func orderedOutcomeOf(assert AssertSpec, pig, pi string) *ScenarioOutcome {
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "ordered", Driver: "rpc-mode", Covers: []string{"x"}, Assert: assert},
		Pig:      SystemResults{System: "pig", Runs: []Result{{System: "pig", Output: pig}}},
		Pi:       SystemResults{System: "pi", Runs: []Result{{System: "pi", Output: pi}}},
	}
	EvaluateOutcome(o)
	return o
}

func TestOrderedJSONAssertionFailsWhereTheValueAssertionPasses(t *testing.T) {
	pig, pi := `{"type":"tool","input":{"path":"a","content":"b"}}`, `{"type":"tool","input":{"content":"b","path":"a"}}`
	if o := orderedOutcomeOf(AssertSpec{JSONOutputEqual: true}, pig, pi); !o.Passed() {
		t.Fatalf("json_output_equal: %s", failures(o))
	}
	o := orderedOutcomeOf(AssertSpec{JSONOutputOrderedEqual: true}, pig, pi)
	if o.Passed() || !strings.Contains(failures(o), "run 1 JSON output: order difference: /0/input: member order pig=") {
		t.Fatalf("json_output_ordered_equal: passed=%t %s", o.Passed(), failures(o))
	}
	if o := orderedOutcomeOf(AssertSpec{JSONOutputOrderedEqual: true}, pig, pig); !o.Passed() {
		t.Fatalf("equal order failed: %s", failures(o))
	}
}

func TestOrderedJSONAssertionMatchesPigOnlyRecordsInOrder(t *testing.T) {
	pi := "{\"type\":\"agent_start\"}\n{\"type\":\"message_start\",\"n\":1}\n"
	ordered := "{\"type\":\"agent_start\"}\n" + pigOnlyRecord + "\n{\"type\":\"message_start\",\"n\":1}\n"
	reordered := "{\"type\":\"agent_start\"}\n{\"followUp\":[],\"steering\":[],\"type\":\"queue_update\"}\n{\"type\":\"message_start\",\"n\":1}\n"
	assert := AssertSpec{JSONOutputOrderedEqual: true, JSONPigOnlyRecords: []JSONPigOnlyRecordRule{pigOnlyRule}}
	if o := orderedOutcomeOf(assert, ordered, pi); !o.Passed() {
		t.Fatalf("the declared divergence failed: %s", failures(o))
	}
	if o := orderedOutcomeOf(assert, reordered, pi); o.Passed() {
		t.Fatal("a pig-only record written in another member order was dropped under json_output_ordered_equal")
	}
}

func TestOrderedJSONAssertionValidation(t *testing.T) {
	sc := func(a AssertSpec) *Scenario {
		return &Scenario{Name: "ordered", Driver: "rpc-mode", Covers: []string{"x"}, Assert: a}
	}
	if err := validate(sc(AssertSpec{JSONOutputOrderedEqual: true})); err != nil {
		t.Fatal(err)
	}
	if err := validate(sc(AssertSpec{JSONOutputOrderedEqual: true, JSONPigOnlyRecords: []JSONPigOnlyRecordRule{pigOnlyRule}})); err != nil {
		t.Fatalf("json_pig_only_records refused under json_output_ordered_equal: %v", err)
	}
	if err := validate(sc(AssertSpec{JSONOutputEqual: true, JSONOutputOrderedEqual: true})); err == nil {
		t.Fatal("both JSON comparisons accepted")
	}
	if err := validate(sc(AssertSpec{JSONOutputOrderedEqual: true, OutputNormalizedEqual: true})); err == nil {
		t.Fatal("text normalization accepted beside json_output_ordered_equal")
	}
	if !scenarioCaptureUse(sc(AssertSpec{JSONOutputOrderedEqual: true})).plain {
		t.Fatal("the ordered JSON comparison does not read the plain capture")
	}
}
