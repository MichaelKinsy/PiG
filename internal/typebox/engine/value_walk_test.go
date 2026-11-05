package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func pendingWalks() int {
	walks.Lock()
	defer walks.Unlock()
	return len(walks.byToken)
}

// TestValueWalkOfASelfReferringSchemaIsARangeError: `{"$ref":"#"}` consumes no part of the value, so a walk without a depth limit recurses until Go's fatal stack overflow, which no
// recover catches and which would end the host process. TypeBox throws a catchable RangeError for the same schema; the host answers it with the same error, for Value.Check and
// Value.Errors, whether the cycle goes through "#" or an $id.
func TestValueWalkOfASelfReferringSchemaIsARangeError(t *testing.T) {
	for _, schema := range []string{`{"$ref":"#"}`, `{"$id":"A","$ref":"A"}`, `{"type":"object","properties":{"a":{"$ref":"#"}}}`} {
		for _, member := range []string{memberValueCheck, memberValueErrors} {
			value := `1`
			if strings.Contains(schema, `"properties"`) {
				value = `{"a":1}`
			}
			reply := serve(t, fmt.Sprintf(`{"member":%q,"args":[%s,%s]}`, member, schema, value))
			if strings.Contains(schema, `"properties"`) {
				// A value that ends the recursion is not an overflow: {"a":1} fails the type of the inner reference, or passes.
				if reply.Error != nil {
					t.Errorf("%s %s: %v, want a result", member, schema, reply.Error)
				}
				continue
			}
			if reply.Error == nil || reply.Error.Name != "RangeError" || reply.Error.Message != "Maximum call stack size exceeded" {
				t.Errorf("%s %s: reply %+v, want RangeError: Maximum call stack size exceeded", member, schema, reply)
			}
		}
	}
	if n := pendingWalks(); n != 0 {
		t.Errorf("%d walks are still registered", n)
	}
}

// TestValueWalkAllowsTheDepthTypeBoxAllows: typebox 1.3.27 checks a value nested 945 objects deep before V8 runs out of stack; a walk that refused earlier than that would reject valid values.
func TestValueWalkAllowsTheDepthTypeBoxAllows(t *testing.T) {
	const depth = 900
	schema, value := `{"type":"number"}`, `1`
	for i := 0; i < depth; i++ {
		schema = `{"type":"object","required":["a"],"properties":{"a":` + schema + `}}`
		value = `{"a":` + value + `}`
	}
	for _, member := range []string{memberValueCheck, memberValueErrors} {
		reply := serve(t, fmt.Sprintf(`{"member":%q,"args":[%s,%s]}`, member, schema, value))
		if reply.Error != nil {
			t.Errorf("%s: %+v", member, reply.Error)
		}
	}
}

// TestValueWalkCostIsLinearInTheRefinementCalls: every refinement call is one round trip that sends the token and one answer; the schema and the value are sent once and the walk is not
// started again. The replay design this replaced sent them again with every answer (4.7 MB and 473 ms for 1,000 items, 18 MB and 1.7 s for 2,000).
func TestValueWalkCostIsLinearInTheRefinementCalls(t *testing.T) {
	const items = 10_000
	schema := json.RawMessage(`{"\u0000":"o","h":{"~kind":"Array"},"e":{"type":"array","items":{"\u0000":"o","h":{"~kind":"Number","~refine":[{"\u0000":"f","i":0}]},"e":{"type":"number"}}}}`)
	list := make([]float64, items)
	for i := range list {
		list[i] = float64(i + 1)
	}
	value, _ := json.Marshal(list)
	start := time.Now()
	result, calls, sent := serveToCompletion(t, memberValueCheck, schema, value, oracleAnswer)
	elapsed := time.Since(start)
	if result != true || len(calls) != items {
		t.Fatalf("Check = %v after %d calls, want true after %d", result, len(calls), items)
	}
	// One request of the schema and the value, then about 60 bytes per answer.
	if limit := len(schema) + len(value) + 200 + items*120; sent > limit {
		t.Errorf("sent %d bytes for %d calls, want at most %d (linear)", sent, items, limit)
	}
	if elapsed > 20*time.Second {
		t.Errorf("%d calls took %v", items, elapsed)
	}
	t.Logf("%d calls: %d bytes sent, %v", items, sent, elapsed)
	if n := pendingWalks(); n != 0 {
		t.Errorf("%d walks are still registered after the walk ended", n)
	}
}

const oneRefinement = `{"\u0000":"o","h":{"~kind":"Number","~refine":[{"\u0000":"f","i":0}]},"e":{"type":"number"}}`

func TestValueWalkCancelAndAnswerErrors(t *testing.T) {
	first := serve(t, `{"member":"Value.Check","args":[`+oneRefinement+`,1]}`)
	if first.Call == nil {
		t.Fatalf("reply %+v, want a call", first)
	}
	if pendingWalks() != 1 {
		t.Fatalf("%d walks registered, want 1", pendingWalks())
	}
	// A string for a check is refused and drops the walk.
	bad := serve(t, fmt.Sprintf(`{"member":"Value.Answer","args":[%q,"yes"]}`, first.Call.Token))
	if bad.Error == nil || bad.Error.Name != "TypeError" || pendingWalks() != 0 {
		t.Errorf("a string answer to a check: %+v, %d walks", bad, pendingWalks())
	}
	// An answer to a walk that is gone fails.
	gone := serve(t, fmt.Sprintf(`{"member":"Value.Answer","args":[%q,true]}`, first.Call.Token))
	if gone.Error == nil {
		t.Errorf("answering a dropped walk: %+v", gone)
	}
	// Cancel drops a parked walk and its goroutine.
	second := serve(t, `{"member":"Value.Check","args":[`+oneRefinement+`,1]}`)
	cancelled := serve(t, fmt.Sprintf(`{"member":"Value.Cancel","args":[%q]}`, second.Call.Token))
	if cancelled.Error != nil || pendingWalks() != 0 {
		t.Errorf("cancel: %+v, %d walks", cancelled, pendingWalks())
	}
	if again := serve(t, `{"member":"Value.Cancel","args":["unknown"]}`); again.Error != nil {
		t.Errorf("cancelling an unknown token: %+v", again)
	}
}

func TestValueWalkIdleLimitAndPendingCap(t *testing.T) {
	old := valueIdleLimit
	valueIdleLimit = 50 * time.Millisecond
	t.Cleanup(func() { valueIdleLimit = old })
	first := serve(t, `{"member":"Value.Check","args":[`+oneRefinement+`,1]}`)
	deadline := time.Now().Add(5 * time.Second)
	for pendingWalks() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pendingWalks() != 0 {
		t.Fatalf("a walk nobody answered is still registered after the idle limit")
	}
	if late := serve(t, fmt.Sprintf(`{"member":"Value.Answer","args":[%q,true]}`, first.Call.Token)); late.Error == nil {
		t.Errorf("an answer after the limit: %+v", late)
	}
	valueIdleLimit = time.Minute
	var tokens []string
	for i := 0; i < maxPendingValueWalks; i++ {
		reply := serve(t, `{"member":"Value.Check","args":[`+oneRefinement+`,1]}`)
		if reply.Call == nil {
			t.Fatalf("walk %d: %+v", i, reply)
		}
		tokens = append(tokens, reply.Call.Token)
	}
	if over := serve(t, `{"member":"Value.Check","args":[`+oneRefinement+`,1]}`); over.Error == nil || over.Error.Name != "RangeError" {
		t.Errorf("one walk past the cap: %+v", over)
	}
	for _, token := range tokens {
		serve(t, fmt.Sprintf(`{"member":"Value.Cancel","args":[%q]}`, token))
	}
	if pendingWalks() != 0 {
		t.Errorf("%d walks left after cancelling every one", pendingWalks())
	}
}
