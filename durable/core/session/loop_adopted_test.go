package session_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// pi: packages/durable/src/session/session.ts #runCommit (8b0caefc, 92ab5360): the Storage adopts the batch inside its call, and the commit is
// published there; the job ends when the call resolves. A job whose commit the Storage adopted holds the line, publishes at once and
// completes only at Answer, whose error (a rejection after adoption) is the job's own.
func TestLoopPublishesAnAdoptedJobAtOnceAndCompletesItAtTheAnswer(t *testing.T) {
	adopted := false
	loop := &session.Loop{Session: &session.Session{}, Adopted: func() bool { return adopted }}
	var order []string
	var got error
	loop.Line(func() error { order = append(order, "run"); adopted = true; return nil }, func(err error) { order = append(order, "done"); got = err })
	loop.Line(func() error { order = append(order, "next"); return nil }, nil)
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(order) != "[run]" {
		t.Fatalf("an adopted job completes only at the Storage's answer: %v", order)
	}
	if loop.Idle() {
		t.Fatal("the line is held until the Storage answers")
	}
	adopted = false
	failure := errors.New("reclaim failed")
	if !loop.Answer(failure) {
		t.Fatal("a job waited for the answer")
	}
	loop.Completer()()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(order) != "[run done next]" || got != failure {
		t.Fatalf("order %v, done got %v: a failure after adoption is the job's error and the next job runs after it", order, got)
	}
	if loop.Answer(nil) {
		t.Fatal("nothing waits for a second answer")
	}
}
