package session_test

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
)

// Upstream commitWith extends the line's Promise chain in its synchronous prefix, so a caller knows the commit's position
// before earlier commits finish. CommitWith reports that moment to a caller-attached lineadmission callback.
func TestCommitReportsLineAdmissionBeforeEarlierCommitsFinish(t *testing.T) {
	harness := open()
	t.Cleanup(func() { _ = harness.Session.Close(ctx) })
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseEarlier := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseEarlier)
	earlier := make(chan error, 1)
	go func() {
		_, err := harness.Session.CommitWith(ctx, func(*session.Transaction) (any, error) {
			close(entered)
			<-release
			return nil, nil
		}, session.TransactionScope{})
		earlier <- err
	}()
	<-entered
	admitted, ran := make(chan struct{}), make(chan struct{})
	later := make(chan error, 1)
	go func() {
		_, err := harness.Session.CommitWith(lineadmission.With(ctx, func() { close(admitted) }), func(*session.Transaction) (any, error) {
			close(ran)
			return nil, nil
		}, session.TransactionScope{})
		later <- err
	}()
	select {
	case <-admitted:
	case <-time.After(10 * time.Second):
		t.Fatal("the commit did not report its admission while an earlier commit held the line")
	}
	select {
	case <-ran:
		t.Fatal("the commit ran before the earlier commit finished")
	default:
	}
	releaseEarlier()
	must(t, <-earlier)
	must(t, <-later)
}
