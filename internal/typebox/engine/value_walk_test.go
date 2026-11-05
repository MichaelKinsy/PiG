package engine

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

func pendingWalks() int { return defaultSession.Parked() }

// serveOn is serve on a session of its own.
func serveOn(t testing.TB, session *Session, request string) valueReply {
	t.Helper()
	var reply valueReply
	if err := json.Unmarshal(session.Serve([]byte(request)), &reply); err != nil {
		t.Fatal(err)
	}
	return reply
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

// depthShapes are the recursive shapes a walk must follow as deep as TypeBox does. typebox 1.3.27 (node 24.19.0) checks these deepest (Value.Check; Value.Errors went 923, 624, 702 and 936
// deep on the same shapes): a Cyclic linked list 937 levels, the same list with a Union([Null, Ref]) link 582, a Cyclic Array(Ref) 702 and nested Type.Object 1092. V8's stack ends the
// walk there; the host's walk, bounded by cycles and MaxFrames, goes on.
var depthShapes = []struct {
	name         string
	schema       string
	nest         func(levels int) string
	typeboxCheck int
}{
	{"linked list", `{"$defs":{"N":{"$id":"N","type":"object","properties":{"next":{"$ref":"N"}}}},"$ref":"N"}`,
		func(n int) string { return strings.Repeat(`{"next":`, n) + `{}` + strings.Repeat(`}`, n) }, 937},
	{"linked list through a union", `{"$defs":{"N":{"$id":"N","type":"object","properties":{"next":{"anyOf":[{"type":"null"},{"$ref":"N"}]}}}},"$ref":"N"}`,
		func(n int) string { return strings.Repeat(`{"next":`, n) + `null` + strings.Repeat(`}`, n) }, 582},
	{"array of itself", `{"$defs":{"N":{"$id":"N","type":"array","items":{"$ref":"N"}}},"$ref":"N"}`,
		func(n int) string { return strings.Repeat(`[`, n) + strings.Repeat(`]`, n) }, 702},
	{"nested objects", ``, func(n int) string { return strings.Repeat(`{"a":`, n) + `1` + strings.Repeat(`}`, n) }, 1092},
}

func nestedObjectsSchema(levels int) string {
	schema := `{"type":"number"}`
	for i := 0; i < levels; i++ {
		schema = `{"type":"object","required":["a"],"properties":{"a":` + schema + `}}`
	}
	return schema
}

// TestValueWalkFollowsRecursionAsDeepAsTypeBox: the walk rejected values TypeBox accepts when it limited the frames it held to 1,000 (498 levels of a linked list against TypeBox's 937).
// Only a real cycle, or a path past the safety net, is a RangeError.
func TestValueWalkFollowsRecursionAsDeepAsTypeBox(t *testing.T) {
	for _, shape := range depthShapes {
		for _, levels := range []int{shape.typeboxCheck, 2 * shape.typeboxCheck, 3000} {
			schema := shape.schema
			if schema == "" {
				schema = nestedObjectsSchema(levels)
			}
			for _, member := range []string{memberValueCheck, memberValueErrors} {
				reply := serve(t, fmt.Sprintf(`{"member":%q,"args":[%s,%s]}`, member, schema, shape.nest(levels)))
				if reply.Error != nil {
					t.Errorf("%s %s at %d levels: %+v, want a result (TypeBox checks %d)", member, shape.name, levels, reply.Error, shape.typeboxCheck)
					continue
				}
				if member == memberValueCheck && string(reply.Value) != "true" {
					t.Errorf("%s at %d levels: %s, want true", shape.name, levels, reply.Value)
				}
			}
		}
	}
}

// TestValueWalkPastTheSafetyNetIsARangeError: a path longer than typebox.MaxFrames ends in the RangeError, not in a process-ending stack overflow. The request's own JSON nesting is limited
// to 10,000 levels, so the walk that reaches the net is the linked list through a union (3 frames a level) at 9,900 levels.
func TestValueWalkPastTheSafetyNetIsARangeError(t *testing.T) {
	shape := depthShapes[1]
	for _, member := range []string{memberValueCheck, memberValueErrors} {
		reply := serve(t, fmt.Sprintf(`{"member":%q,"args":[%s,%s]}`, member, shape.schema, shape.nest(9900)))
		if reply.Error == nil || reply.Error.Name != "RangeError" {
			t.Errorf("%s: %+v %+v, want the RangeError", member, reply, reply.Error)
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

func startWalk(t testing.TB, session *Session) valueReply {
	t.Helper()
	reply := serveOn(t, session, `{"member":"Value.Check","args":[`+oneRefinement+`,1]}`)
	if reply.Call == nil {
		t.Fatalf("a walk with a refinement did not park: %+v", reply)
	}
	return reply
}

// waitForGoroutines waits until the process runs no more than want goroutines.
func waitForGoroutines(t testing.TB, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > want {
		t.Fatalf("%d goroutines, want at most %d: a parked walk outlived its session", got, want)
	}
}

// TestSessionCloseCancelsItsParkedWalks: a walk belongs to the session of the connection that started it. Closing the session (the host does it when the connection closes, on a
// reload, a crash or the end of the session) cancels every walk it holds, however many an extension abandoned, and leaves no goroutine behind; the answer of an extension that lives on
// is refused for a walk that is gone.
func TestSessionCloseCancelsItsParkedWalks(t *testing.T) {
	before := runtime.NumGoroutine()
	session := NewSession()
	var tokens []string
	for i := 0; i < 500; i++ {
		tokens = append(tokens, startWalk(t, session).Call.Token)
	}
	if session.Parked() != 500 {
		t.Fatalf("%d walks parked, want 500", session.Parked())
	}
	session.Close()
	session.Close()
	if session.Parked() != 0 {
		t.Errorf("%d walks left after Close", session.Parked())
	}
	waitForGoroutines(t, before)
	if late := serveOn(t, session, fmt.Sprintf(`{"member":"Value.Answer","args":[%q,true]}`, tokens[0])); late.Error == nil {
		t.Errorf("an answer after Close: %+v", late)
	}
	if refused := serveOn(t, session, `{"member":"Value.Check","args":[`+oneRefinement+`,1]}`); refused.Error == nil {
		t.Errorf("a walk started on a closed session: %+v", refused)
	}
}

// TestSessionsAreIndependent: the walks an extension abandons are its own. They neither count against another connection's session nor answer to its tokens, and closing one session
// leaves the other's walks parked and answerable.
func TestSessionsAreIndependent(t *testing.T) {
	noisy, quiet := NewSession(), NewSession()
	t.Cleanup(noisy.Close)
	t.Cleanup(quiet.Close)
	for i := 0; i < 1000; i++ {
		startWalk(t, noisy)
	}
	mine := startWalk(t, quiet)
	if stolen := serveOn(t, noisy, fmt.Sprintf(`{"member":"Value.Answer","args":[%q,true]}`, mine.Call.Token)); stolen.Error == nil {
		t.Errorf("a session answered the walk of another: %+v", stolen)
	}
	noisy.Close()
	done := serveOn(t, quiet, fmt.Sprintf(`{"member":"Value.Answer","args":[%q,true]}`, mine.Call.Token))
	if done.Error != nil || string(done.Value) != "true" {
		t.Errorf("the quiet session's walk after the noisy one closed: %+v", done)
	}
}

// TestValueAnswerRacingCancelNeverHangs: a cancel (or a close) that lands while an answer is in flight must not leave the answer's host call waiting for an event the cancelled walk
// never sends.
func TestValueAnswerRacingCancelNeverHangs(t *testing.T) {
	for i := 0; i < 3000; i++ {
		session := NewSession()
		first := startWalk(t, session)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			session.Serve([]byte(fmt.Sprintf(`{"member":"Value.Answer","args":[%q,true]}`, first.Call.Token)))
		}()
		// Let the answer get as far as the walk's own steps before the cancel, a little further each round.
		for spin := 0; spin < i%1500; spin++ {
			runtime.Gosched()
		}
		if i%2 == 0 {
			session.Serve([]byte(fmt.Sprintf(`{"member":"Value.Cancel","args":[%q]}`, first.Call.Token)))
		} else {
			session.Close()
		}
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Fatalf("iteration %d: the answer's host call never returned", i)
		}
		session.Close()
	}
}

// TestValueCancelAndAnswerNeedTheirArguments: a request without arguments is an error, not an index panic (a fused Go extension calls Serve directly, with no dispatcher to recover it).
func TestValueCancelAndAnswerNeedTheirArguments(t *testing.T) {
	for _, member := range []string{"Value.Cancel", "Value.Answer", "Value.Check", "Value.Errors"} {
		reply := serve(t, `{"member":"`+member+`","args":[]}`)
		if reply.Error == nil || reply.Error.Name != "TypeError" {
			t.Errorf("%s with no arguments: %+v, want a TypeError", member, reply)
		}
	}
}
