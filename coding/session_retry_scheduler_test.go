package coding

import "testing"

// _prepareRetry resolves its Promise after the sleep reaction. A caller awaiting it resumes in a separate reaction behind already queued command responses.
func TestRetryCompletionUsesSeparateReaction(t *testing.T) {
	session := &Session{}
	var reactions []func()
	wait := &sessionRetryWait{
		session: session,
		ctx:     t.Context(),
		done:    make(chan retryWaitResult, 1),
		schedule: func(reaction func()) {
			reactions = append(reactions, reaction)
		},
	}
	session.retryCancel = wait
	wait.resolve(true)
	wait.resolve(false)
	select {
	case <-wait.done:
		t.Fatal("retry resumed inside its resolving reaction")
	default:
	}
	if len(reactions) != 1 || session.retryCancel != nil {
		t.Fatalf("completion reactions=%d active retry=%v", len(reactions), session.retryCancel != nil)
	}
	reactions[0]()
	result := <-wait.done
	if !result.retry || result.err != nil {
		t.Fatalf("result=%+v", result)
	}
}
